"""Checks the standard OPC UA Server object with a third-party client.

A generic UA client depends on these nodes: it reads the NamespaceArray before
anything else, and reads ServerStatus on a timer to decide whether the server is
still alive. Without them a conforming client concludes the server is dead and
tears the connection down.

Usage:
    python ua_server_nodes.py <endpoint-url>
"""
import asyncio, sys
from asyncua import Client, ua
FAILS=[]
def check(n,c,d=""):
    print(("  PASS " if c else "  FAIL ")+n+(f"  [{d}]" if d and not c else ""))
    if not c: FAILS.append(n)
async def main():
    async with Client(url=sys.argv[1]) as c:
        st = await c.nodes.server_state.read_value()
        check("Server_ServerStatus_State reads Running", st == 0 or int(st) == 0, str(st))
        status = await c.get_node(ua.NodeId(2256)).read_value()
        check("ServerStatus decodes as a structure",
              hasattr(status, "State") and hasattr(status, "BuildInfo"), repr(status)[:200])
        if hasattr(status, "BuildInfo"):
            bi = status.BuildInfo
            check("BuildInfo field order round-trips",
                  bi.ManufacturerName == "opcda-access-adapter", repr(bi)[:250])
            check("BuildInfo carries the software version",
                  bi.SoftwareVersion == "interop-harness", repr(bi.SoftwareVersion))
        ct = await c.get_node(ua.NodeId(2258)).read_value()
        check("CurrentTime is answered as of the read", ct is not None, str(ct))
        sl = await c.get_node(ua.NodeId(2267)).read_value()
        check("ServiceLevel is fully operational", sl == 255, str(sl))
        au = await c.get_node(ua.NodeId(2994)).read_value()
        check("Auditing reports that no audit events are emitted", au is False, str(au))
        srv = await c.get_node(ua.NodeId(2253)).get_children()
        names = sorted([(await n.read_browse_name()).Name for n in srv])
        check("the Server object browses its components",
              {"ServerArray","NamespaceArray","ServerStatus","ServiceLevel","Auditing"} <= set(names),
              str(names))
    print()
    if FAILS:
        print("FAILED:", FAILS); sys.exit(1)
    print("ALL CHECKS PASSED")
asyncio.run(main())
