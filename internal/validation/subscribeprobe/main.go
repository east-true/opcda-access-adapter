// Command subscribeprobe exercises the DA Subscribe core against a real local
// OPC DA server. No frontend exposes Subscribe, so the probe drives the DA
// runtime directly. It is validation tooling only and never logs a process
// value; only DA metadata, counts, and identifiers are printed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

const (
	maximumProbeTimeout = 5 * time.Minute
	// Repeating the subscribe/unsubscribe cycle more times than
	// MaxSubscriptions proves each cycle actually removed its DA group; a leak
	// would exhaust the limit instead.
	cleanupCycles = 24
)

var probeItems = []opcda.DAItemID{"Test/Int32", "Test/Float", "Test/String"}

// The fixture's Test items are static, so a subscription only receives the
// server's initial snapshot unless something changes a value. Test/Float is the
// fixture's read/write VT_R4 item, so the probe writes distinct values through
// the ordinary typed Write path to require change-driven callbacks.
const changeItem = opcda.DAItemID("Test/Float")

const changeNotifications = 3

func main() {
	clsid := flag.String("clsid", "", "exact configured source CLSID")
	updateRate := flag.Duration("update-rate", 250*time.Millisecond, "requested DA group update rate")
	serverProcess := flag.String("server-process", "", "fixture process name to terminate for the disconnect scenario")
	timeout := flag.Duration("timeout", 3*time.Minute, "bounded scenario deadline")
	flag.Parse()
	if flag.NArg() != 0 || *clsid == "" || *timeout <= 0 || *timeout > maximumProbeTimeout {
		fmt.Fprintln(os.Stderr, "usage: subscribeprobe -clsid CLSID [-update-rate DURATION] [-server-process NAME] [-timeout DURATION]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	limits := opcda.DefaultLimits()
	runtime, err := opcda.New(opcda.Config{
		Source: opcda.SourceConfig{CLSID: *clsid},
		Limits: limits,
		// Write is enabled only inside this validation process, so the probe can
		// change a source value and require change-driven notifications.
		WriteEnabled:     true,
		ReconnectInitial: 200 * time.Millisecond,
		ReconnectMax:     2 * time.Second,
	})
	if err != nil {
		fail("start DA runtime", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			fail("shutdown DA runtime", err)
		}
	}()

	if err := runProbe(ctx, runtime, limits, *updateRate, *serverProcess); err != nil {
		fail("validate DA Subscribe core", err)
	}
	fmt.Printf(
		"SUBSCRIBE_REAL_DA_PASS items=%d changeNotifications=%d cleanupCycles=%d disconnectTested=%t valuesLogged=false\n",
		len(probeItems), changeNotifications, cleanupCycles, *serverProcess != "",
	)
}

func runProbe(ctx context.Context, runtime opcda.Runtime, limits opcda.Limits, updateRate time.Duration, serverProcess string) error {
	if err := waitConnected(ctx, runtime); err != nil {
		return err
	}
	if !runtime.Status(ctx).Capabilities.Subscribe {
		return errors.New("connected runtime did not advertise the Subscribe capability")
	}

	subscription, err := subscribeAll(ctx, runtime, updateRate)
	if err != nil {
		return err
	}
	info := subscription.Info()
	if info.RevisedUpdateRate <= 0 {
		return errors.New("the source did not report a revised update rate")
	}
	fmt.Printf(
		"subscribed id=%s generation=%d requestedRate=%s revisedRate=%s activeItems=%d\n",
		info.ID, info.ConnectionGeneration, info.RequestedUpdateRate, info.RevisedUpdateRate, info.ActiveItemCount,
	)
	if count := runtime.Status(ctx).SubscriptionCount; count != 1 {
		return fmt.Errorf("status reported %d subscriptions, want 1", count)
	}

	// The server's initial snapshot proves the advise and the callback vtable.
	if err := awaitNotifications(ctx, subscription, info.ActiveItemCount, 1); err != nil {
		return err
	}
	// A value change proves notifications are driven by the source, not by a
	// one-time snapshot.
	if err := awaitChangeNotifications(ctx, runtime, subscription, info.ActiveItemCount); err != nil {
		return err
	}

	if err := verifyUnsubscribeStopsDelivery(ctx, runtime, subscription, updateRate); err != nil {
		return err
	}
	if count := runtime.Status(ctx).SubscriptionCount; count != 0 {
		return fmt.Errorf("status reported %d subscriptions after unsubscribe, want 0", count)
	}

	if err := verifyCleanupCycles(ctx, runtime, limits, updateRate); err != nil {
		return err
	}
	if serverProcess == "" {
		return nil
	}
	return verifyDisconnectInvalidation(ctx, runtime, updateRate, serverProcess)
}

func waitConnected(ctx context.Context, runtime opcda.Runtime) error {
	for {
		status := runtime.Status(ctx)
		switch status.State {
		case opcda.RuntimeStateConnected:
			return nil
		case opcda.RuntimeStateDegraded:
			return fmt.Errorf("runtime degraded before connecting: %s", status.DegradedReason)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("runtime did not reach the connected state: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func subscribeAll(ctx context.Context, runtime opcda.Runtime, updateRate time.Duration) (opcda.Subscription, error) {
	subscription, err := runtime.Subscribe(ctx, opcda.SubscribeRequest{
		Items:               probeItems,
		RequestedUpdateRate: updateRate,
	})
	if err != nil {
		return nil, fmt.Errorf("Subscribe: %w", err)
	}
	info := subscription.Info()
	if len(info.Items) != len(probeItems) {
		return nil, fmt.Errorf("Subscribe reported %d item statuses, want %d", len(info.Items), len(probeItems))
	}
	for index, item := range info.Items {
		if item.ItemID != probeItems[index] {
			return nil, fmt.Errorf("item %d is %q, want the exact requested ItemID %q", index, item.ItemID, probeItems[index])
		}
		if !item.Active {
			return nil, fmt.Errorf("item %q was not activated: hresult=%s code=%s", item.ItemID, item.HRESULT.Hex(), item.ErrorCode)
		}
		if item.CanonicalType == nil || item.AccessRights == nil {
			return nil, fmt.Errorf("item %q lost its canonical VARTYPE or access rights", item.ItemID)
		}
	}
	if info.ActiveItemCount != len(probeItems) {
		return nil, fmt.Errorf("ActiveItemCount is %d, want %d", info.ActiveItemCount, len(probeItems))
	}
	return subscription, nil
}

// awaitNotifications requires real OnDataChange batches from the source and
// checks that every entry keeps exact DA metadata and respects the coalescing
// bound. Process values are inspected but never printed.
func awaitNotifications(ctx context.Context, subscription opcda.Subscription, activeItems int, batches int) error {
	known := make(map[opcda.DAItemID]struct{}, len(probeItems))
	for _, itemID := range probeItems {
		known[itemID] = struct{}{}
	}
	seen := make(map[opcda.DAItemID]int)
	valued := 0

	for batch := 0; batch < batches; batch++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("source delivered %d of %d OnDataChange batches: %w", batch, batches, ctx.Err())
		case <-subscription.Done():
			return fmt.Errorf("subscription was invalidated while awaiting notifications: %v", subscription.Err())
		case <-subscription.Updates():
		}
		values := subscription.Drain()
		if len(values) == 0 {
			batch--
			continue
		}
		// The pending set holds at most one entry per active item, so a drained
		// batch can never exceed the subscription's item count.
		if len(values) > activeItems {
			return fmt.Errorf("drained %d values, more than the %d active items", len(values), activeItems)
		}
		for _, value := range values {
			if _, ok := known[value.ItemID]; !ok {
				return fmt.Errorf("notification carried unexpected ItemID %q", value.ItemID)
			}
			if !value.HRESULTPresent {
				return fmt.Errorf("notification for %q carried no HRESULT", value.ItemID)
			}
			if value.VarType == nil || value.CanonicalType == nil || value.AccessRights == nil {
				return fmt.Errorf("notification for %q lost DA metadata", value.ItemID)
			}
			seen[value.ItemID]++
			if value.HRESULT.Failed() {
				if value.Value != nil {
					return fmt.Errorf("failed item %q carried a value", value.ItemID)
				}
				continue
			}
			if value.ErrorCode != "" {
				return fmt.Errorf("succeeding item %q reported adapter error %s", value.ItemID, value.ErrorCode)
			}
			if value.Value == nil {
				return fmt.Errorf("succeeding item %q carried no value", value.ItemID)
			}
			if value.Value.ItemID != value.ItemID || value.Value.VarType != *value.VarType {
				return fmt.Errorf("item %q lost its identity or VARTYPE inside the value", value.ItemID)
			}
			valued++
		}
	}
	if valued == 0 {
		return errors.New("the source delivered notifications but no readable value")
	}
	for itemID, count := range seen {
		fmt.Printf("notified itemId=%s batches=%d\n", itemID, count)
	}
	return nil
}

// awaitChangeNotifications writes distinct values to the fixture's read/write
// item and requires the source to report each change. Written and reported
// values are never printed.
func awaitChangeNotifications(ctx context.Context, runtime opcda.Runtime, subscription opcda.Subscription, activeItems int) error {
	for change := 0; change < changeNotifications; change++ {
		// Drop anything already pending so the awaited batch is the one caused
		// by this write.
		subscription.Drain()

		written := float32(change+1) + 0.5
		results, err := runtime.WriteBatch(ctx, []opcda.WriteItem{
			{ItemID: changeItem, VarType: opcda.VTR4, Value: written},
		})
		if err != nil {
			return fmt.Errorf("write to induce change %d: %w", change+1, err)
		}
		if len(results) != 1 || results[0].ItemID != changeItem {
			return fmt.Errorf("change %d write returned an unexpected result set", change+1)
		}
		if results[0].HRESULT.Failed() || results[0].ErrorCode != "" {
			return fmt.Errorf("change %d write failed: hresult=%s code=%s",
				change+1, results[0].HRESULT.Hex(), results[0].ErrorCode)
		}

		if err := awaitItem(ctx, subscription, changeItem, activeItems); err != nil {
			return fmt.Errorf("change %d was not reported by the source: %w", change+1, err)
		}
	}
	fmt.Printf("change-driven notifications verified itemId=%s changes=%d\n", changeItem, changeNotifications)
	return nil
}

// awaitItem waits for a drained batch that contains the given item.
func awaitItem(ctx context.Context, subscription opcda.Subscription, itemID opcda.DAItemID, activeItems int) error {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		select {
		case <-deadline.Done():
			return deadline.Err()
		case <-subscription.Done():
			return fmt.Errorf("subscription was invalidated: %v", subscription.Err())
		case <-subscription.Updates():
		}
		values := subscription.Drain()
		if len(values) > activeItems {
			return fmt.Errorf("drained %d values, more than the %d active items", len(values), activeItems)
		}
		for _, value := range values {
			if value.ItemID != itemID {
				continue
			}
			if !value.HRESULTPresent || value.VarType == nil || value.CanonicalType == nil {
				return fmt.Errorf("change notification for %q lost DA metadata", itemID)
			}
			if value.HRESULT.Failed() {
				return fmt.Errorf("change notification for %q reported %s", itemID, value.HRESULT.Hex())
			}
			if value.Value == nil {
				return fmt.Errorf("change notification for %q carried no value", itemID)
			}
			return nil
		}
	}
}

func verifyUnsubscribeStopsDelivery(ctx context.Context, runtime opcda.Runtime, subscription opcda.Subscription, updateRate time.Duration) error {
	id := subscription.Info().ID
	if err := runtime.Unsubscribe(ctx, id); err != nil {
		return fmt.Errorf("Unsubscribe: %w", err)
	}
	select {
	case <-subscription.Done():
	case <-time.After(5 * time.Second):
		return errors.New("Unsubscribe did not invalidate the subscription")
	}
	if err := requireInvalidated(subscription.Err()); err != nil {
		return err
	}
	if values := subscription.Drain(); values != nil {
		return fmt.Errorf("an unsubscribed subscription delivered %d pending values", len(values))
	}

	// Several update-rate intervals must produce nothing once the advise is
	// dropped and the group removed.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(4 * updateRate):
	}
	if values := subscription.Drain(); values != nil {
		return fmt.Errorf("the source still delivered %d values after Unsubscribe", len(values))
	}

	err := runtime.Unsubscribe(ctx, id)
	adapterErr, ok := opcda.AsAdapterError(err)
	if !ok || adapterErr.Code != opcda.CodeSubscriptionNotFound {
		return fmt.Errorf("repeated Unsubscribe returned %v, want %s", err, opcda.CodeSubscriptionNotFound)
	}
	return nil
}

// verifyCleanupCycles subscribes and unsubscribes more times than the
// configured maximum. A leaked DA group or advise cookie exhausts the limit.
func verifyCleanupCycles(ctx context.Context, runtime opcda.Runtime, limits opcda.Limits, updateRate time.Duration) error {
	identifiers := make(map[opcda.SubscriptionID]struct{}, cleanupCycles)
	for cycle := 0; cycle < cleanupCycles; cycle++ {
		subscription, err := runtime.Subscribe(ctx, opcda.SubscribeRequest{
			Items:               probeItems,
			RequestedUpdateRate: updateRate,
		})
		if err != nil {
			return fmt.Errorf("cleanup cycle %d of %d failed to subscribe with limit %d: %w",
				cycle+1, cleanupCycles, limits.MaxSubscriptions, err)
		}
		id := subscription.Info().ID
		if _, duplicate := identifiers[id]; duplicate {
			return fmt.Errorf("subscription identifier %s was reused", id)
		}
		identifiers[id] = struct{}{}
		if err := runtime.Unsubscribe(ctx, id); err != nil {
			return fmt.Errorf("cleanup cycle %d failed to unsubscribe: %w", cycle+1, err)
		}
	}
	if count := runtime.Status(ctx).SubscriptionCount; count != 0 {
		return fmt.Errorf("status reported %d subscriptions after the cleanup cycles, want 0", count)
	}
	fmt.Printf("cleanup cycles completed cycles=%d maxSubscriptions=%d leaked=0\n", cleanupCycles, limits.MaxSubscriptions)
	return nil
}

// verifyDisconnectInvalidation terminates the fixture server, requires that the
// subscription is invalidated rather than silently retained, and requires that
// reconnect restores nothing implicitly.
func verifyDisconnectInvalidation(ctx context.Context, runtime opcda.Runtime, updateRate time.Duration, serverProcess string) error {
	subscription, err := subscribeAll(ctx, runtime, updateRate)
	if err != nil {
		return err
	}
	before := subscription.Info()
	if err := awaitNotifications(ctx, subscription, before.ActiveItemCount, 1); err != nil {
		return err
	}

	if err := terminateProcess(serverProcess); err != nil {
		return err
	}
	// Disconnect detection is deliberately conservative: it happens on the next
	// COM call rather than through active polling, so the probe issues one.
	forceDetection(ctx, runtime)

	select {
	case <-subscription.Done():
	case <-ctx.Done():
		return fmt.Errorf("source loss did not invalidate the subscription: %w", ctx.Err())
	}
	if err := requireInvalidated(subscription.Err()); err != nil {
		return err
	}
	if values := subscription.Drain(); values != nil {
		return fmt.Errorf("an invalidated subscription delivered %d values from the previous generation", len(values))
	}

	if err := waitConnected(ctx, runtime); err != nil {
		return fmt.Errorf("runtime did not reconnect after the induced outage: %w", err)
	}
	if count := runtime.Status(ctx).SubscriptionCount; count != 0 {
		return fmt.Errorf("reconnect implicitly restored %d subscriptions, want 0", count)
	}
	err = runtime.Unsubscribe(ctx, before.ID)
	adapterErr, ok := opcda.AsAdapterError(err)
	if !ok || adapterErr.Code != opcda.CodeSubscriptionNotFound {
		return fmt.Errorf("a subscription from the previous generation survived reconnect: %v", err)
	}

	// Resubscribing must be explicit and must produce a new generation-scoped
	// identity rather than resuming the old one.
	after, err := subscribeAll(ctx, runtime, updateRate)
	if err != nil {
		return fmt.Errorf("explicit resubscribe after reconnect: %w", err)
	}
	afterInfo := after.Info()
	if afterInfo.ID == before.ID {
		return fmt.Errorf("resubscribe reused identifier %s across connection generations", afterInfo.ID)
	}
	if afterInfo.ConnectionGeneration <= before.ConnectionGeneration {
		return fmt.Errorf("resubscribe generation %d did not advance past %d",
			afterInfo.ConnectionGeneration, before.ConnectionGeneration)
	}
	if err := awaitNotifications(ctx, after, afterInfo.ActiveItemCount, 1); err != nil {
		return fmt.Errorf("resubscribed callback did not deliver: %w", err)
	}
	if err := runtime.Unsubscribe(ctx, afterInfo.ID); err != nil {
		return fmt.Errorf("final Unsubscribe: %w", err)
	}
	fmt.Printf(
		"disconnect invalidation verified oldId=%s oldGeneration=%d newId=%s newGeneration=%d\n",
		before.ID, before.ConnectionGeneration, afterInfo.ID, afterInfo.ConnectionGeneration,
	)
	return nil
}

func forceDetection(ctx context.Context, runtime opcda.Runtime) {
	detectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// The outcome is intentionally ignored; the call exists to let the runtime
	// observe the lost source.
	_, _ = runtime.ReadBatch(detectCtx, opcda.ReadRequest{Items: probeItems[:1], Source: opcda.DADataSourceDevice})
}

func requireInvalidated(err error) error {
	adapterErr, ok := opcda.AsAdapterError(err)
	if !ok || adapterErr.Code != opcda.CodeSubscriptionInvalidated {
		return fmt.Errorf("invalidation cause was %v, want %s", err, opcda.CodeSubscriptionInvalidated)
	}
	return nil
}

func terminateProcess(name string) error {
	command := exec.Command("taskkill", "/F", "/IM", name+".exe")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("terminate fixture process %s: %w: %s", name, err, string(output))
	}
	fmt.Printf("terminated fixture process name=%s\n", name)
	return nil
}

func fail(operation string, err error) {
	fmt.Fprintf(os.Stderr, "SUBSCRIBE_REAL_DA_FAIL %s: %v\n", operation, err)
	os.Exit(1)
}
