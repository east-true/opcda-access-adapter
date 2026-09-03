"""Third-party OPC UA client conformance run against the adapter's UA frontend.

Every assertion here is made by a third-party client's decoder, not by this
project's. That is the whole point: this project's encoder and decoder agree
with each other by construction, so a round trip against itself cannot catch a
field both sides get wrong the same way. Four defects were found this way that
the Go suite could not see, including an OPN reply that named no security
policy and a Publish that answered immediately and turned a conforming client
into a busy loop.

asyncua is used here as an interop client only. Design §5.2 names it among the
projects that "may be a reference or interop target but are not adopted as an
implementation base", and nothing in the adapter links against it.

Usage:
    python ua_client_conformance.py [endpoint-url] [--write] [--browseless]

Run it against internal/validation/uainterop, which serves the UA frontend over
a scripted DA source. See docs/validation/ua-client-interop.md.
"""
import asyncio, sys, datetime
from asyncua import Client, ua

FAILS = []
PASSES = 0
def check(name, cond, detail=""):
    global PASSES
    print(("  PASS " if cond else "  FAIL ") + name + (f"  [{detail}]" if detail and not cond else ""))
    if cond: PASSES += 1
    else: FAILS.append(f"{name}: {detail}")

URL = sys.argv[1] if len(sys.argv) > 1 else "opc.tcp://127.0.0.1:48401"
WRITE = "--write" in sys.argv
BROWSELESS = "--browseless" in sys.argv
# What internal/validation/uainterop configures. They are deliberately
# different: an ApplicationUri names a deployment, a namespace URI has to stay
# stable across restarts.
APPLICATION_URI = "urn:opcda-access-adapter:uainterop"
SOURCE_NAMESPACE_URI = "urn:opcda-access-adapter:uainterop:source"

# Resolved from the server's namespace array at start-up. A client that hard
# codes an index is doing what design §35.2 forbids -- treating the index as
# identity rather than the URI -- and this one used to: it assumed 1, which was
# right until OPC 10000-5 8.3.2 put the ApplicationUri there.
ADAPTER_NS = None

def nid(item_id):
    return ua.NodeId(f"item:{item_id}", ADAPTER_NS, ua.NodeIdType.String)

async def main():
    global ADAPTER_NS
    async with Client(url=URL) as c:
        ns = await c.get_namespace_array()
        # 8.3.2: "index 0 is reserved for the OPC UA namespace, and index 1 is
        # reserved for the local Server", and the ApplicationUri is that entry.
        check("namespace 0 is the OPC UA namespace",
              len(ns) > 0 and ns[0] == "http://opcfoundation.org/UA/", str(ns))
        check("namespace 1 is the local server",
              len(ns) > 1 and ns[1] == APPLICATION_URI, str(ns))
        check("the adapter's namespace follows the reserved two",
              SOURCE_NAMESPACE_URI in ns and ns.index(SOURCE_NAMESPACE_URI) >= 2, str(ns))
        if SOURCE_NAMESPACE_URI in ns:
            ADAPTER_NS = ns.index(SOURCE_NAMESPACE_URI)
        else:
            print("  the adapter namespace is absent; nothing else can be addressed")
            return 1

        # --- endpoints ---
        eps = await c.get_endpoints()
        check("GetEndpoints returns one endpoint", len(eps) == 1, str(len(eps)))
        if eps:
            ep = eps[0]
            check("endpoint policy is None",
                  ep.SecurityPolicyUri.endswith("#None"), ep.SecurityPolicyUri)
            check("endpoint offers an anonymous user token",
                  any(t.TokenType == ua.UserTokenType.Anonymous for t in ep.UserIdentityTokens))

        # --- browse ---
        if not BROWSELESS:
            print("\n[browse]")
            objs = c.nodes.objects
            kids = {(await k.read_browse_name()).Name: k for k in await objs.get_children()}
            check("Objects holds the source folder", "ScriptedSource" in kids, str(list(kids)))
            check("Objects holds the standard Server object", "Server" in kids, str(list(kids)))
            src = kids.get("ScriptedSource")
            branches = {(await b.read_browse_name()).Name: b for b in await src.get_children()}
            for want in ("Types", "Quality", "Rights", "Timestamp", "Odd", "Simulation"):
                check(f"branch {want} is browsable", want in branches, str(list(branches)))
            leaves = await branches["Types"].get_children()
            check("Types branch exposes every scripted item", len(leaves) == 13, str(len(leaves)))
            # A browsed node's identifier must carry the exact ItemID.
            got = {n.nodeid.Identifier for n in leaves}
            check("browsed node identifiers carry the exact ItemID",
                  "item:Types.Bool" in got and "item:Types.String" in got, str(sorted(got))[:200])
            # Hierarchical browse with subtypes, the way a generic client walks.
            hier = await src.get_children(refs=ua.ObjectIds.HierarchicalReferences)
            check("HierarchicalReferences+subtypes finds the branches", len(hier) >= 6, str(len(hier)))

        # --- read: every mapped VARTYPE, judged by asyncua's decoder ---
        print("\n[types]")
        expect = {
            "Types.Bool":   (ua.VariantType.Boolean, True),
            "Types.SByte":  (ua.VariantType.SByte,  -8),
            "Types.Byte":   (ua.VariantType.Byte,    200),
            "Types.Int16":  (ua.VariantType.Int16,  -1234),
            "Types.UInt16": (ua.VariantType.UInt16,  60000),
            "Types.Int32":  (ua.VariantType.Int32,  -70000),
            "Types.UInt32": (ua.VariantType.UInt32,  4000000000),
            "Types.Int64":  (ua.VariantType.Int64,  -5000000000),
            "Types.UInt64": (ua.VariantType.UInt64,  18000000000000000000),
            "Types.Float":  (ua.VariantType.Float,   1.5),
            "Types.Double": (ua.VariantType.Double,  2.25),
            "Types.String": (ua.VariantType.String,  "hello"),
            "Types.Int32Again": (ua.VariantType.Int32, -70001),
        }
        for item, (vtype, value) in expect.items():
            dv = await c.get_node(nid(item)).read_data_value(raise_on_bad_status=False)
            ok = dv.StatusCode.is_good() and dv.Value.VariantType == vtype and dv.Value.Value == value
            check(f"{item} decodes as {vtype.name}", ok,
                  f"{dv.StatusCode} {dv.Value.VariantType} {dv.Value.Value!r}")

        # --- quality: Part 8 Table A.3 ---
        #
        # LAST_KNOWN is the one row this adapter does not take from Table A.3,
        # and the deviation is recorded rather than accidental: Table 61 says
        # the fieldbus code Bad_LastKnown "shall be mapped to
        # Uncertain_NoCommunicationLastUsable", because a Bad severity must
        # return a Null value, which would discard the last known value
        # LAST_KNOWN exists to carry. scripts/spec-check/check.py records the
        # same deviation against Table A.3, and docs/opcua-mapping.md explains
        # it. Asserting Table A.3 here would ask a foreign client to confirm a
        # mapping this server deliberately does not implement.
        print("\n[quality]")
        qual = {
            "Quality.Bad":           ua.StatusCodes.BadNotConnected if False else 0x80000000,
            "Quality.Uncertain":     0x40000000,
            "Quality.LastKnown":     0x408F0000,
            "Quality.OutOfService":  ua.StatusCodes.BadOutOfService,
        }
        for item, code in qual.items():
            dv = await c.get_node(nid(item)).read_data_value(raise_on_bad_status=False)
            actual = dv.StatusCode.value
            if item in ("Quality.Bad", "Quality.Uncertain"):
                ok = (actual & 0xC0000000) == code
                detail = f"got 0x{actual:08X}, wanted severity 0x{code:08X}"
            else:
                ok = actual == code
                detail = f"got 0x{actual:08X}, wanted 0x{code:08X}"
            check(f"{item} maps through Table A.3", ok, detail)
        # Table A.3 maps LOCAL_OVERRIDE to Good_LocalOverride: a Good severity
        # that still carries the override condition.
        dv = await c.get_node(nid("Quality.LocalOverride")).read_data_value(raise_on_bad_status=False)
        check("Quality.LocalOverride maps to Good_LocalOverride",
              dv.StatusCode.value == ua.StatusCodes.GoodLocalOverride,
              f"0x{dv.StatusCode.value:08X}")

        # --- timestamps ---
        print("\n[timestamps]")
        dv = await c.get_node(nid("Types.Int32")).read_data_value(raise_on_bad_status=False)
        check("a DA timestamp becomes the SourceTimestamp",
              dv.SourceTimestamp == datetime.datetime(2024,3,14,15,9,26,535000,
                                                      tzinfo=datetime.timezone.utc)
              or dv.SourceTimestamp.replace(tzinfo=None) == datetime.datetime(2024,3,14,15,9,26,535000),
              str(dv.SourceTimestamp))
        dv = await c.get_node(nid("Timestamp.Absent")).read_data_value(raise_on_bad_status=False)
        check("an absent DA timestamp is not invented",
              dv.SourceTimestamp is None, str(dv.SourceTimestamp))

        # --- access rights, which DA reports in AddItems and never in Browse ---
        print("\n[rights]")
        dv = await c.get_node(nid("Rights.WriteOnly")).read_data_value(raise_on_bad_status=False)
        check("a write-only item refuses the read with the source's answer",
              dv.StatusCode.value == ua.StatusCodes.BadNotReadable, str(dv.StatusCode))
        dv = await c.get_node(nid("Rights.ReadOnly")).read_data_value(raise_on_bad_status=False)
        check("a read-only item reads", dv.StatusCode.is_good() and dv.Value.Value == 7,
              str(dv.StatusCode))

        # --- exact ItemID preservation ---
        print("\n[identity]")
        odd = {"Odd.Item With Spaces": 10, "Odd/Slash.Separated": 11,
               "Odd.온도": 21.5, "Odd.MiXeD.CaSe": 13}
        for item, value in odd.items():
            dv = await c.get_node(nid(item)).read_data_value(raise_on_bad_status=False)
            check(f"exact ItemID survives the round trip: {item!r}",
                  dv.StatusCode.is_good() and dv.Value.Value == value,
                  f"{dv.StatusCode} {dv.Value.Value!r}")
        dv = await c.get_node(ua.NodeId("item:No.Such.Item", 1)).read_data_value(raise_on_bad_status=False)
        check("an unknown item is Bad_NodeIdUnknown",
              dv.StatusCode.value == ua.StatusCodes.BadNodeIdUnknown, str(dv.StatusCode))

        # --- attributes ---
        print("\n[attributes]")
        node = c.get_node(nid("Types.Double"))
        cls = await node.read_node_class()
        check("a DA item is a Variable", cls == ua.NodeClass.Variable, str(cls))
        dt = await node.read_data_type()
        check("its DataType is Double after a read taught the type",
              dt == ua.NodeId(ua.ObjectIds.Double), str(dt))
        check("its ValueRank is scalar",
              (await node.read_value_rank()) == ua.ValueRank.Scalar)

        # --- write ---
        print("\n[write]")
        target = c.get_node(nid("Writable.Setpoint"))
        try:
            await target.write_value(ua.DataValue(ua.Variant(73.5, ua.VariantType.Double)))
            wrote = True; err = ""
        except Exception as e:
            wrote = False; err = str(e)
        if WRITE:
            check("a permitted write reaches the source", wrote, err)
            dv = await target.read_data_value(raise_on_bad_status=False)
            check("the written value reads back", dv.Value.Value == 73.5, str(dv.Value.Value))
        else:
            check("write is refused when the adapter disables it", not wrote, "the write was accepted")

        # --- subscription ---
        print("\n[subscription]")
        seen = []
        class Handler:
            def datachange_notification(self, node, val, data):
                seen.append((node.nodeid.Identifier, val))
        sub = await c.create_subscription(500, Handler())
        handles = await sub.subscribe_data_change(
            [c.get_node(nid("Simulation.Ramp")), c.get_node(nid("Simulation.Counter"))])
        deadline = asyncio.get_event_loop().time() + 20
        while len({i for i, _ in seen}) < 2 and asyncio.get_event_loop().time() < deadline:
            await asyncio.sleep(0.2)
        check("the client received changes for both monitored items",
              len({i for i, _ in seen}) == 2, str(seen[:6]))
        before = len(seen)
        await asyncio.sleep(3)
        check("changes keep arriving while the subscription lives",
              len(seen) > before, f"{before} -> {len(seen)}")
        await sub.delete()

    print()
    if FAILS:
        print(f"FAILED {len(FAILS)}")
        for f in FAILS: print("  -", f)
        sys.exit(1)
    print(f"ALL CHECKS PASSED  asyncua checks={PASSES}")

asyncio.run(main())
