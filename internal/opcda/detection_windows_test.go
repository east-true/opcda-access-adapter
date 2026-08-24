//go:build windows

package opcda

import (
	"context"
	"testing"
	"time"
	"unsafe"
)

func TestLocalDetectionGUIDsAndVTableLayouts(t *testing.T) {
	if got, want := clsidStandardComponentCategoriesManager.String(), "{0002E005-0000-0000-C000-000000000046}"; got != want {
		t.Fatalf("category manager CLSID = %s, want %s", got, want)
	}
	if got, want := iidICatInformation.String(), "{0002E013-0000-0000-C000-000000000046}"; got != want {
		t.Fatalf("ICatInformation IID = %s, want %s", got, want)
	}
	if got, want := catidOPCDAServer20.String(), OPCDAServer20CategoryID; got != want {
		t.Fatalf("OPC DA 2.0 CATID = %s, want %s", got, want)
	}
	pointerSize := unsafe.Sizeof(uintptr(0))
	var categories iCatInformationVTable
	if got, want := unsafe.Offsetof(categories.EnumClassesOfCategories), uintptr(5)*pointerSize; got != want {
		t.Fatalf("EnumClassesOfCategories offset = %d, want %d", got, want)
	}
	var enumerator iEnumGUIDVTable
	if got, want := unsafe.Offsetof(enumerator.Next), uintptr(3)*pointerSize; got != want {
		t.Fatalf("IEnumGUID::Next offset = %d, want %d", got, want)
	}
}

func TestLocalDetectionInitializesAndCleansCOM(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The machine-wide registration inventory may change independently on a
	// shared runner. Repeated success and structurally valid results exercise
	// COM initialization, interface release, and task-memory cleanup without
	// assuming that external registry state is immutable.
	for iteration := 0; iteration < 20; iteration++ {
		servers, err := DetectLocalServers(ctx, LocalDetectionLimits{})
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		for _, server := range servers {
			if server.CLSID == "" {
				t.Fatal("detected registration has no CLSID")
			}
		}
	}
}
