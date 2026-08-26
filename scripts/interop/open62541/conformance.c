/* Third-party OPC UA client conformance run using open62541.
 *
 * A second foreign client, independent of the first. asyncua is Python and
 * hand-rolls its codec; open62541 is C and is one of the reference
 * implementations, so the two disagree with this adapter in different ways if
 * it is wrong. Design §5.2 names open62541 among the projects that may be a
 * reference or interop target but are not adopted as an implementation base,
 * and nothing in the adapter links against it.
 *
 * Usage: conformance <endpoint-url> [--write] [--browseless]
 */
#include <open62541/client.h>
#include <open62541/client_config_default.h>
#include <open62541/client_highlevel.h>
#include <open62541/client_subscriptions.h>

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int failures = 0;

static void check(const char *name, int condition, const char *detail) {
    if(condition) {
        printf("  PASS %s\n", name);
        return;
    }
    printf("  FAIL %s  [%s]\n", name, detail ? detail : "");
    failures++;
}

/* itemNode builds the node identifier for a DA item. The identifier carries the
 * exact ItemID verbatim, which is the adapter's central promise about identity;
 * this client is a foreign witness to it surviving the round trip. */
static UA_NodeId itemNode(const char *itemID) {
    char buffer[512];
    snprintf(buffer, sizeof(buffer), "item:%s", itemID);
    return UA_NODEID_STRING_ALLOC(1, buffer);
}

static UA_StatusCode readValue(UA_Client *client, const char *itemID, UA_Variant *out) {
    UA_NodeId node = itemNode(itemID);
    UA_ReadValueId valueID;
    UA_ReadValueId_init(&valueID);
    valueID.nodeId = node;
    valueID.attributeId = UA_ATTRIBUTEID_VALUE;

    UA_ReadRequest request;
    UA_ReadRequest_init(&request);
    request.nodesToRead = &valueID;
    request.nodesToReadSize = 1;
    request.timestampsToReturn = UA_TIMESTAMPSTORETURN_BOTH;

    UA_ReadResponse response = UA_Client_Service_read(client, request);
    UA_StatusCode status = response.responseHeader.serviceResult;
    if(status == UA_STATUSCODE_GOOD && response.resultsSize == 1) {
        status = response.results[0].hasStatus ? response.results[0].status : UA_STATUSCODE_GOOD;
        if(out && response.results[0].hasValue)
            UA_Variant_copy(&response.results[0].value, out);
    }
    UA_ReadResponse_clear(&response);
    UA_NodeId_clear(&node);
    return status;
}

/* readDataValue keeps the whole DataValue, so the timestamps can be judged. */
static UA_DataValue readDataValue(UA_Client *client, const char *itemID) {
    UA_NodeId node = itemNode(itemID);
    UA_ReadValueId valueID;
    UA_ReadValueId_init(&valueID);
    valueID.nodeId = node;
    valueID.attributeId = UA_ATTRIBUTEID_VALUE;

    UA_ReadRequest request;
    UA_ReadRequest_init(&request);
    request.nodesToRead = &valueID;
    request.nodesToReadSize = 1;
    request.timestampsToReturn = UA_TIMESTAMPSTORETURN_BOTH;

    UA_DataValue copy;
    UA_DataValue_init(&copy);
    UA_ReadResponse response = UA_Client_Service_read(client, request);
    if(response.responseHeader.serviceResult == UA_STATUSCODE_GOOD && response.resultsSize == 1)
        UA_DataValue_copy(&response.results[0], &copy);
    UA_ReadResponse_clear(&response);
    UA_NodeId_clear(&node);
    return copy;
}

struct typeCase {
    const char *itemID;
    const UA_DataType *type;
    const char *label;
};

static void checkTypes(UA_Client *client) {
    /* Every VARTYPE the adapter maps, judged by open62541's decoder against the
     * UA built-in type OPC 10000-8 Table A.2 names. VT_DATE is the row worth
     * watching: the table maps it to Double, not DateTime. */
    struct typeCase cases[] = {
        {"Types.Bool",   &UA_TYPES[UA_TYPES_BOOLEAN], "Boolean"},
        {"Types.SByte",  &UA_TYPES[UA_TYPES_SBYTE],   "SByte"},
        {"Types.Byte",   &UA_TYPES[UA_TYPES_BYTE],    "Byte"},
        {"Types.Int16",  &UA_TYPES[UA_TYPES_INT16],   "Int16"},
        {"Types.UInt16", &UA_TYPES[UA_TYPES_UINT16],  "UInt16"},
        {"Types.Int32",  &UA_TYPES[UA_TYPES_INT32],   "Int32"},
        {"Types.UInt32", &UA_TYPES[UA_TYPES_UINT32],  "UInt32"},
        {"Types.Int64",  &UA_TYPES[UA_TYPES_INT64],   "Int64"},
        {"Types.UInt64", &UA_TYPES[UA_TYPES_UINT64],  "UInt64"},
        {"Types.Float",  &UA_TYPES[UA_TYPES_FLOAT],   "Float"},
        {"Types.Double", &UA_TYPES[UA_TYPES_DOUBLE],  "Double"},
        {"Types.String", &UA_TYPES[UA_TYPES_STRING],  "String"},
        {"Types.Date",   &UA_TYPES[UA_TYPES_DOUBLE],  "Double (Table A.2 maps VT_DATE to Double)"},
    };
    char name[256], detail[256];
    for(size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
        UA_Variant value;
        UA_Variant_init(&value);
        UA_StatusCode status = readValue(client, cases[i].itemID, &value);
        int ok = status == UA_STATUSCODE_GOOD &&
                 UA_Variant_hasScalarType(&value, cases[i].type);
        snprintf(name, sizeof(name), "%s decodes as %s", cases[i].itemID, cases[i].label);
        snprintf(detail, sizeof(detail), "status 0x%08X", status);
        check(name, ok, detail);
        UA_Variant_clear(&value);
    }
}

static void checkQuality(UA_Client *client) {
    /* Raw DA qualities mapped through OPC 10000-8 Table A.3. */
    UA_StatusCode status = readValue(client, "Quality.Bad", NULL);
    char detail[128];
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("Quality.Bad carries a Bad severity", (status & 0xC0000000u) == 0x80000000u, detail);

    status = readValue(client, "Quality.Uncertain", NULL);
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("Quality.Uncertain carries an Uncertain severity",
          (status & 0xC0000000u) == 0x40000000u, detail);

    status = readValue(client, "Quality.LastKnown", NULL);
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("Quality.LastKnown maps to Bad_OutOfService",
          status == UA_STATUSCODE_BADOUTOFSERVICE, detail);

    status = readValue(client, "Quality.OutOfService", NULL);
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("Quality.OutOfService maps to Bad_OutOfService",
          status == UA_STATUSCODE_BADOUTOFSERVICE, detail);

    /* Table A.3 maps LOCAL_OVERRIDE to Good_LocalOverride, which is a Good
     * severity carrying the override condition rather than a plain Good. The
     * distinction is the point: the client is told the value is overridden and
     * still told it is usable. */
    status = readValue(client, "Quality.LocalOverride", NULL);
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("Quality.LocalOverride maps to Good_LocalOverride",
          status == UA_STATUSCODE_GOODLOCALOVERRIDE, detail);
}

static void checkTimestamps(UA_Client *client) {
    UA_DataValue present = readDataValue(client, "Types.Int32");
    check("a DA timestamp becomes the SourceTimestamp",
          present.hasSourceTimestamp && present.sourceTimestamp != 0, "no source timestamp");
    UA_DataValue_clear(&present);

    /* A DA server need not report a timestamp, and the adapter must leave the
     * SourceTimestamp unset rather than substituting its own clock. */
    UA_DataValue absent = readDataValue(client, "Timestamp.Absent");
    check("an absent DA timestamp is not invented",
          !absent.hasSourceTimestamp || absent.sourceTimestamp == 0, "a source timestamp appeared");
    UA_DataValue_clear(&absent);
}

static void checkRights(UA_Client *client) {
    /* DA reports access rights in AddItems, never in Browse, so these prove the
     * answer a client sees is the source's. */
    UA_StatusCode status = readValue(client, "Rights.WriteOnly", NULL);
    char detail[128];
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("a write-only item refuses the read with the source's answer",
          status == UA_STATUSCODE_BADNOTREADABLE, detail);

    UA_Variant value;
    UA_Variant_init(&value);
    status = readValue(client, "Rights.ReadOnly", &value);
    check("a read-only item reads", status == UA_STATUSCODE_GOOD, "not readable");
    UA_Variant_clear(&value);
}

static void checkIdentity(UA_Client *client) {
    /* ItemIDs a naive implementation would normalise. Reading them back through
     * a foreign client proves the exact bytes survived the round trip. */
    const char *odd[] = {
        "Odd.Item With Spaces", "Odd/Slash.Separated", "Odd.\xEC\x98\xA8\xEB\x8F\x84",
        "Odd.MiXeD.CaSe",
    };
    char name[256];
    for(size_t i = 0; i < sizeof(odd) / sizeof(odd[0]); i++) {
        UA_Variant value;
        UA_Variant_init(&value);
        UA_StatusCode status = readValue(client, odd[i], &value);
        snprintf(name, sizeof(name), "exact ItemID survives the round trip: '%s'", odd[i]);
        check(name, status == UA_STATUSCODE_GOOD, "not readable");
        UA_Variant_clear(&value);
    }

    UA_StatusCode status = readValue(client, "No.Such.Item", NULL);
    char detail[128];
    snprintf(detail, sizeof(detail), "0x%08X", status);
    check("an unknown item is Bad_NodeIdUnknown",
          status == UA_STATUSCODE_BADNODEIDUNKNOWN, detail);
}

/* checkServerObject reads the standard nodes a generic client depends on. */
static void checkServerObject(UA_Client *client) {
    UA_Variant value;
    UA_Variant_init(&value);
    UA_StatusCode status = UA_Client_readValueAttribute(
        client, UA_NS0ID(SERVER_SERVERSTATUS_STATE), &value);
    check("Server_ServerStatus_State reads",
          status == UA_STATUSCODE_GOOD, "not readable");
    UA_Variant_clear(&value);

    UA_Variant_init(&value);
    status = UA_Client_readValueAttribute(
        client, UA_NS0ID(SERVER_SERVERSTATUS), &value);
    int decoded = status == UA_STATUSCODE_GOOD &&
                  UA_Variant_hasScalarType(&value, &UA_TYPES[UA_TYPES_SERVERSTATUSDATATYPE]);
    check("ServerStatus decodes as a ServerStatusDataType structure", decoded, "not decoded");
    if(decoded) {
        UA_ServerStatusDataType *serverStatus = (UA_ServerStatusDataType *)value.data;
        check("ServerStatus reports Running",
              serverStatus->state == UA_SERVERSTATE_RUNNING, "not running");
        /* The BuildInfo field order is the NodeSet's and a foreign decoder is
         * the only thing that can confirm it. */
        check("BuildInfo field order round-trips",
              serverStatus->buildInfo.manufacturerName.length > 0 &&
                  strncmp((const char *)serverStatus->buildInfo.manufacturerName.data,
                          "opcda-access-adapter",
                          serverStatus->buildInfo.manufacturerName.length) == 0,
              "manufacturer name did not land in its own field");
        check("ServerStatus carries a StartTime", serverStatus->startTime != 0, "no start time");
        check("ServerStatus carries a CurrentTime", serverStatus->currentTime != 0, "no current time");
    }
    UA_Variant_clear(&value);

    UA_Variant_init(&value);
    status = UA_Client_readValueAttribute(client, UA_NS0ID(SERVER_NAMESPACEARRAY), &value);
    int isArray = status == UA_STATUSCODE_GOOD &&
                  UA_Variant_hasArrayType(&value, &UA_TYPES[UA_TYPES_STRING]) &&
                  value.arrayLength == 2;
    check("the NamespaceArray is a two entry String array", isArray, "not an array of two");
    UA_Variant_clear(&value);
}

static size_t notifications = 0;

static void onDataChange(UA_Client *client, UA_UInt32 subId, void *subContext,
                         UA_UInt32 monId, void *monContext, UA_DataValue *value) {
    (void)client; (void)subId; (void)subContext; (void)monId; (void)monContext; (void)value;
    notifications++;
}

static void checkSubscription(UA_Client *client) {
    UA_CreateSubscriptionRequest request = UA_CreateSubscriptionRequest_default();
    request.requestedPublishingInterval = 500;
    UA_CreateSubscriptionResponse response =
        UA_Client_Subscriptions_create(client, request, NULL, NULL, NULL);
    if(response.responseHeader.serviceResult != UA_STATUSCODE_GOOD) {
        check("a subscription is created", 0, "CreateSubscription failed");
        return;
    }
    check("a subscription is created", 1, NULL);

    const char *items[] = {"Simulation.Ramp", "Simulation.Counter"};
    for(size_t i = 0; i < 2; i++) {
        UA_NodeId node = itemNode(items[i]);
        UA_MonitoredItemCreateRequest monitored = UA_MonitoredItemCreateRequest_default(node);
        UA_MonitoredItemCreateResult result = UA_Client_MonitoredItems_createDataChange(
            client, response.subscriptionId, UA_TIMESTAMPSTORETURN_BOTH,
            monitored, NULL, onDataChange, NULL);
        char name[128];
        snprintf(name, sizeof(name), "a monitored item is created for %s", items[i]);
        check(name, result.statusCode == UA_STATUSCODE_GOOD, "CreateMonitoredItems failed");
        UA_MonitoredItemCreateResult_clear(&result);
        UA_NodeId_clear(&node);
    }

    /* A Publish that is answered immediately turns a conforming client into a
     * busy loop, and starves the sampling. Both are visible here: the count has
     * to keep climbing over several seconds. */
    for(int i = 0; i < 8; i++)
        UA_Client_run_iterate(client, 500);
    size_t first = notifications;
    check("changes arrive for the monitored items", first > 0, "no notification arrived");

    for(int i = 0; i < 8; i++)
        UA_Client_run_iterate(client, 500);
    char detail[128];
    snprintf(detail, sizeof(detail), "%zu -> %zu", first, notifications);
    check("changes keep arriving while the subscription lives", notifications > first, detail);

    UA_Client_Subscriptions_deleteSingle(client, response.subscriptionId);
}

static void checkBrowse(UA_Client *client) {
    /* A hierarchical browse with subtypes included is how a generic client
     * walks an address space. A server that decodes includeSubtypes and then
     * ignores it answers this with nothing at all. */
    UA_BrowseRequest request;
    UA_BrowseRequest_init(&request);
    request.requestedMaxReferencesPerNode = 0;
    request.nodesToBrowse = UA_BrowseDescription_new();
    request.nodesToBrowseSize = 1;
    request.nodesToBrowse[0].nodeId = UA_NS0ID(OBJECTSFOLDER);
    request.nodesToBrowse[0].browseDirection = UA_BROWSEDIRECTION_FORWARD;
    request.nodesToBrowse[0].referenceTypeId = UA_NS0ID(HIERARCHICALREFERENCES);
    request.nodesToBrowse[0].includeSubtypes = true;
    request.nodesToBrowse[0].resultMask = UA_BROWSERESULTMASK_ALL;

    UA_BrowseResponse response = UA_Client_Service_browse(client, request);
    int sawSource = 0, sawServer = 0;
    if(response.responseHeader.serviceResult == UA_STATUSCODE_GOOD && response.resultsSize == 1) {
        for(size_t i = 0; i < response.results[0].referencesSize; i++) {
            UA_ReferenceDescription *reference = &response.results[0].references[i];
            if(reference->browseName.name.length == strlen("ScriptedSource") &&
               strncmp((const char *)reference->browseName.name.data, "ScriptedSource",
                       reference->browseName.name.length) == 0)
                sawSource = 1;
            if(reference->browseName.name.length == strlen("Server") &&
               strncmp((const char *)reference->browseName.name.data, "Server",
                       reference->browseName.name.length) == 0)
                sawServer = 1;
        }
    }
    check("a hierarchical browse with subtypes finds the source folder", sawSource, "not found");
    check("a hierarchical browse with subtypes finds the Server object", sawServer, "not found");
    UA_BrowseRequest_clear(&request);
    UA_BrowseResponse_clear(&response);
}

static void checkWrite(UA_Client *client, int writeEnabled) {
    UA_NodeId node = itemNode("Writable.Setpoint");
    UA_Double setpoint = 73.5;
    UA_Variant value;
    UA_Variant_init(&value);
    UA_Variant_setScalar(&value, &setpoint, &UA_TYPES[UA_TYPES_DOUBLE]);
    UA_StatusCode status = UA_Client_writeValueAttribute(client, node, &value);
    UA_NodeId_clear(&node);

    char detail[128];
    snprintf(detail, sizeof(detail), "0x%08X", status);
    if(writeEnabled) {
        check("a permitted write reaches the source", status == UA_STATUSCODE_GOOD, detail);
        UA_Variant readBack;
        UA_Variant_init(&readBack);
        if(readValue(client, "Writable.Setpoint", &readBack) == UA_STATUSCODE_GOOD &&
           UA_Variant_hasScalarType(&readBack, &UA_TYPES[UA_TYPES_DOUBLE])) {
            check("the written value reads back",
                  *(UA_Double *)readBack.data == 73.5, "a different value came back");
        } else {
            check("the written value reads back", 0, "not readable");
        }
        UA_Variant_clear(&readBack);
    } else {
        check("write is refused when the adapter disables it",
              status != UA_STATUSCODE_GOOD, "the write was accepted");
    }
}

int main(int argc, char *argv[]) {
    const char *endpoint = argc > 1 ? argv[1] : "opc.tcp://127.0.0.1:48411";
    int writeEnabled = 0, browseless = 0;
    for(int i = 2; i < argc; i++) {
        if(strcmp(argv[i], "--write") == 0) writeEnabled = 1;
        if(strcmp(argv[i], "--browseless") == 0) browseless = 1;
    }

    UA_Client *client = UA_Client_new();
    UA_ClientConfig_setDefault(UA_Client_getConfig(client));

    UA_StatusCode status = UA_Client_connect(client, endpoint);
    if(status != UA_STATUSCODE_GOOD) {
        printf("  FAIL connect  [0x%08X %s]\n", status, UA_StatusCode_name(status));
        UA_Client_delete(client);
        return 1;
    }
    check("connect, open a secure channel, and activate a session", 1, NULL);

    printf("\n[server object]\n");
    checkServerObject(client);
    if(!browseless) {
        printf("\n[browse]\n");
        checkBrowse(client);
    }
    printf("\n[types]\n");
    checkTypes(client);
    printf("\n[quality]\n");
    checkQuality(client);
    printf("\n[timestamps]\n");
    checkTimestamps(client);
    printf("\n[rights]\n");
    checkRights(client);
    printf("\n[identity]\n");
    checkIdentity(client);
    printf("\n[write]\n");
    checkWrite(client, writeEnabled);
    printf("\n[subscription]\n");
    checkSubscription(client);

    UA_Client_disconnect(client);
    UA_Client_delete(client);

    printf("\n");
    if(failures > 0) {
        printf("FAILED %d\n", failures);
        return 1;
    }
    printf("ALL CHECKS PASSED\n");
    return 0;
}
