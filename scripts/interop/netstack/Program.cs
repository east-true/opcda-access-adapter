// Third-party OPC UA client conformance run using the OPC Foundation's own
// .NET Standard stack.
//
// This is the reference implementation published by the OPC Foundation itself,
// so where it and this adapter disagree, the adapter is the one to look at
// first. It is a third independent judge: asyncua hand-rolls its codec in
// Python, open62541 is C, and this is the Foundation's own. Design §5.2 allows
// all three as interop targets and none of them is an implementation
// dependency — nothing in the adapter links against any of them.
//
// Usage: dotnet run -- <endpoint-url> [--write] [--browseless]
using Opc.Ua;
using Opc.Ua.Client;
using Opc.Ua.Configuration;

internal static class Program
{
    private static int _failures;

    private static void Check(string name, bool condition, string? detail = null)
    {
        if (condition)
        {
            Console.WriteLine($"  PASS {name}");
            return;
        }
        Console.WriteLine($"  FAIL {name}  [{detail}]");
        _failures++;
    }

    // ItemNode builds the identifier for a DA item. It carries the exact
    // ItemID verbatim, which is the adapter's central promise about identity.
    private static NodeId ItemNode(string itemId) => new NodeId("item:" + itemId, 1);

    private static async Task<int> Main(string[] args)
    {
        var endpointUrl = args.Length > 0 ? args[0] : "opc.tcp://127.0.0.1:48411";
        var writeEnabled = args.Contains("--write");
        var browseless = args.Contains("--browseless");

        // The stack insists on a PKI layout even for an unsecured endpoint, so
        // a throwaway one is created beside the binary.
        var pki = Path.Combine(Path.GetTempPath(), "opcda-adapter-interop-pki");
        Directory.CreateDirectory(pki);

        var config = new ApplicationConfiguration
        {
            ApplicationName = "opcda-access-adapter interop",
            ApplicationUri = "urn:opcda-access-adapter:interop:netstack",
            ApplicationType = ApplicationType.Client,
            SecurityConfiguration = new SecurityConfiguration
            {
                // Only SecurityPolicy None is served, so no certificate is
                // involved. ADR-0016 forbids describing that as production
                // ready and this run does not claim otherwise. The stores are
                // still configured because the stack validates the
                // configuration before it looks at the endpoint's policy.
                ApplicationCertificate = new CertificateIdentifier
                {
                    StoreType = CertificateStoreType.Directory,
                    StorePath = Path.Combine(pki, "own"),
                    SubjectName = "CN=opcda-access-adapter interop",
                },
                TrustedIssuerCertificates = new CertificateTrustList
                {
                    StoreType = CertificateStoreType.Directory,
                    StorePath = Path.Combine(pki, "issuer"),
                },
                TrustedPeerCertificates = new CertificateTrustList
                {
                    StoreType = CertificateStoreType.Directory,
                    StorePath = Path.Combine(pki, "trusted"),
                },
                RejectedCertificateStore = new CertificateTrustList
                {
                    StoreType = CertificateStoreType.Directory,
                    StorePath = Path.Combine(pki, "rejected"),
                },
                AutoAcceptUntrustedCertificates = true,
                RejectSHA1SignedCertificates = false,
                MinimumCertificateKeySize = 1024,
            },
            TransportConfigurations = new TransportConfigurationCollection(),
            TransportQuotas = new TransportQuotas { OperationTimeout = 30000 },
            ClientConfiguration = new ClientConfiguration { DefaultSessionTimeout = 60000 },
            TraceConfiguration = new TraceConfiguration(),
        };
        await config.Validate(ApplicationType.Client).ConfigureAwait(false);

        var endpointDescription = CoreClientUtils.SelectEndpoint(config, endpointUrl, useSecurity: false);
        var endpointConfiguration = EndpointConfiguration.Create(config);
        var endpoint = new ConfiguredEndpoint(null, endpointDescription, endpointConfiguration);

        using var session = await Session.Create(
            config, endpoint, updateBeforeConnect: false,
            sessionName: "interop", sessionTimeout: 60000,
            identity: new UserIdentity(new AnonymousIdentityToken()), preferredLocales: null)
            .ConfigureAwait(false);

        Check("connect, open a secure channel, and activate a session", session.Connected);
        Check("the endpoint publishes SecurityPolicy None",
            endpointDescription.SecurityPolicyUri.EndsWith("#None"),
            endpointDescription.SecurityPolicyUri);
        Check("the namespace table carries the adapter namespace",
            session.NamespaceUris.Count >= 2, $"{session.NamespaceUris.Count} namespaces");

        Console.WriteLine("\n[server object]");
        CheckServerObject(session);

        if (!browseless)
        {
            Console.WriteLine("\n[browse]");
            CheckBrowse(session);
        }

        Console.WriteLine("\n[types]");
        CheckTypes(session);
        Console.WriteLine("\n[quality]");
        CheckQuality(session);
        Console.WriteLine("\n[timestamps]");
        CheckTimestamps(session);
        Console.WriteLine("\n[rights]");
        CheckRights(session);
        Console.WriteLine("\n[identity]");
        CheckIdentity(session);
        Console.WriteLine("\n[write]");
        CheckWrite(session, writeEnabled);
        Console.WriteLine("\n[subscription]");
        CheckSubscription(session);

        await session.CloseAsync().ConfigureAwait(false);

        Console.WriteLine();
        if (_failures > 0)
        {
            Console.WriteLine($"FAILED {_failures}");
            return 1;
        }
        Console.WriteLine("ALL CHECKS PASSED");
        return 0;
    }

    private static DataValue ReadItem(Session session, string itemId) =>
        ReadNode(session, ItemNode(itemId));

    private static DataValue ReadNode(Session session, NodeId node)
    {
        var toRead = new ReadValueIdCollection
        {
            new ReadValueId { NodeId = node, AttributeId = Attributes.Value },
        };
        session.Read(null, 0, TimestampsToReturn.Both, toRead,
            out DataValueCollection results, out _);
        return results[0];
    }

    private static void CheckTypes(Session session)
    {
        // Every VARTYPE the adapter can actually deliver, judged by the
        // Foundation's own decoder against the built-in type OPC 10000-8
        // Table A.2 names.
        var cases = new (string Item, Type Expected, object Value)[]
        {
            ("Types.Bool", typeof(bool), true),
            ("Types.SByte", typeof(sbyte), (sbyte)-8),
            ("Types.Byte", typeof(byte), (byte)200),
            ("Types.Int16", typeof(short), (short)-1234),
            ("Types.UInt16", typeof(ushort), (ushort)60000),
            ("Types.Int32", typeof(int), -70000),
            ("Types.UInt32", typeof(uint), 4000000000u),
            ("Types.Int64", typeof(long), -5000000000L),
            ("Types.UInt64", typeof(ulong), 18000000000000000000UL),
            ("Types.Float", typeof(float), 1.5f),
            ("Types.Double", typeof(double), 2.25d),
            ("Types.String", typeof(string), "hello"),
            ("Types.Int32Again", typeof(int), -70001),
        };
        foreach (var (item, expected, value) in cases)
        {
            var read = ReadItem(session, item);
            var ok = StatusCode.IsGood(read.StatusCode) &&
                     read.Value is not null &&
                     read.Value.GetType() == expected &&
                     read.Value.Equals(value);
            Check($"{item} decodes as {expected.Name}", ok,
                $"{read.StatusCode} {read.Value?.GetType().Name} {read.Value}");
        }
    }

    private static void CheckQuality(Session session)
    {
        // Raw DA qualities mapped through OPC 10000-8 Table A.3.
        var bad = ReadItem(session, "Quality.Bad").StatusCode;
        Check("Quality.Bad carries a Bad severity", StatusCode.IsBad(bad), bad.ToString());

        var uncertain = ReadItem(session, "Quality.Uncertain").StatusCode;
        Check("Quality.Uncertain carries an Uncertain severity",
            StatusCode.IsUncertain(uncertain), uncertain.ToString());

        // LAST_KNOWN and OUT_OF_SERVICE share the Bad_OutOfService row.
        var lastKnown = ReadItem(session, "Quality.LastKnown").StatusCode;
        Check("Quality.LastKnown maps to Bad_OutOfService",
            lastKnown.Code == StatusCodes.BadOutOfService, lastKnown.ToString());

        var outOfService = ReadItem(session, "Quality.OutOfService").StatusCode;
        Check("Quality.OutOfService maps to Bad_OutOfService",
            outOfService.Code == StatusCodes.BadOutOfService, outOfService.ToString());

        // Table A.3 maps LOCAL_OVERRIDE to Good_LocalOverride: a Good severity
        // that still carries the override condition.
        var localOverride = ReadItem(session, "Quality.LocalOverride").StatusCode;
        Check("Quality.LocalOverride maps to Good_LocalOverride",
            localOverride.Code == StatusCodes.GoodLocalOverride, localOverride.ToString());
    }

    private static void CheckTimestamps(Session session)
    {
        var present = ReadItem(session, "Types.Int32");
        Check("a DA timestamp becomes the SourceTimestamp",
            present.SourceTimestamp != DateTime.MinValue, present.SourceTimestamp.ToString("O"));

        // A DA server need not report a timestamp, and the adapter must leave
        // the SourceTimestamp unset rather than substituting its own clock.
        var absent = ReadItem(session, "Timestamp.Absent");
        Check("an absent DA timestamp is not invented",
            absent.SourceTimestamp == DateTime.MinValue, absent.SourceTimestamp.ToString("O"));
    }

    private static void CheckRights(Session session)
    {
        // DA reports access rights in AddItems, never in Browse, so these
        // prove the answer a client sees is the source's.
        var writeOnly = ReadItem(session, "Rights.WriteOnly").StatusCode;
        Check("a write-only item refuses the read with the source's answer",
            writeOnly.Code == StatusCodes.BadNotReadable, writeOnly.ToString());

        var readOnly = ReadItem(session, "Rights.ReadOnly");
        Check("a read-only item reads",
            StatusCode.IsGood(readOnly.StatusCode) && readOnly.Value is int value && value == 7,
            readOnly.StatusCode.ToString());
    }

    private static void CheckIdentity(Session session)
    {
        // ItemIDs a naive implementation would normalise. Reading them back
        // proves the exact bytes survived the round trip through a NodeId.
        var odd = new (string Item, object Value)[]
        {
            ("Odd.Item With Spaces", 10),
            ("Odd/Slash.Separated", 11),
            ("Odd.온도", 21.5d),
            ("Odd.MiXeD.CaSe", 13),
        };
        foreach (var (item, value) in odd)
        {
            var read = ReadItem(session, item);
            Check($"exact ItemID survives the round trip: '{item}'",
                StatusCode.IsGood(read.StatusCode) && Equals(read.Value, value),
                $"{read.StatusCode} {read.Value}");
        }

        var unknown = ReadItem(session, "No.Such.Item").StatusCode;
        Check("an unknown item is Bad_NodeIdUnknown",
            unknown.Code == StatusCodes.BadNodeIdUnknown, unknown.ToString());
    }

    private static void CheckServerObject(Session session)
    {
        var state = ReadNode(session, VariableIds.Server_ServerStatus_State);
        Check("Server_ServerStatus_State reads Running",
            StatusCode.IsGood(state.StatusCode) && Convert.ToInt32(state.Value) == 0,
            $"{state.StatusCode} {state.Value}");

        // ServerStatus is a structure. The Foundation's own decoder resolving
        // it into ServerStatusDataType with its fields in the right places is
        // the strongest available confirmation of the NodeSet field order.
        var status = ReadNode(session, VariableIds.Server_ServerStatus);
        var decoded = StatusCode.IsGood(status.StatusCode) &&
                      status.Value is ExtensionObject extension &&
                      extension.Body is ServerStatusDataType;
        Check("ServerStatus decodes as a ServerStatusDataType structure", decoded,
            $"{status.StatusCode} {status.Value?.GetType().Name}");
        if (decoded)
        {
            var body = (ServerStatusDataType)((ExtensionObject)status.Value).Body;
            Check("ServerStatus reports Running", body.State == ServerState.Running,
                body.State.ToString());
            Check("BuildInfo field order round-trips",
                body.BuildInfo?.ManufacturerName == "opcda-access-adapter",
                $"manufacturer '{body.BuildInfo?.ManufacturerName}', product '{body.BuildInfo?.ProductName}'");
            Check("ServerStatus carries a StartTime",
                body.StartTime != DateTime.MinValue, body.StartTime.ToString("O"));
            Check("ServerStatus carries a CurrentTime",
                body.CurrentTime != DateTime.MinValue, body.CurrentTime.ToString("O"));
        }

        var namespaces = ReadNode(session, VariableIds.Server_NamespaceArray);
        Check("the NamespaceArray is a two entry String array",
            StatusCode.IsGood(namespaces.StatusCode) &&
            namespaces.Value is string[] uris && uris.Length == 2,
            namespaces.StatusCode.ToString());
    }

    private static void CheckBrowse(Session session)
    {
        // A hierarchical browse with subtypes included is how a generic client
        // walks an address space. A server that ignores includeSubtypes
        // answers this with nothing at all.
        var toBrowse = new BrowseDescriptionCollection
        {
            new BrowseDescription
            {
                NodeId = ObjectIds.ObjectsFolder,
                BrowseDirection = BrowseDirection.Forward,
                ReferenceTypeId = ReferenceTypeIds.HierarchicalReferences,
                IncludeSubtypes = true,
                ResultMask = (uint)BrowseResultMask.All,
            },
        };
        session.Browse(null, null, 0, toBrowse, out BrowseResultCollection results, out _);
        var names = results[0].References.Select(r => r.BrowseName.Name).ToList();
        Check("a hierarchical browse with subtypes finds the source folder",
            names.Contains("ScriptedSource"), string.Join(",", names));
        Check("a hierarchical browse with subtypes finds the Server object",
            names.Contains("Server"), string.Join(",", names));
    }

    private static void CheckWrite(Session session, bool writeEnabled)
    {
        var toWrite = new WriteValueCollection
        {
            new WriteValue
            {
                NodeId = ItemNode("Writable.Setpoint"),
                AttributeId = Attributes.Value,
                Value = new DataValue(new Variant(73.5d)),
            },
        };
        StatusCode status;
        try
        {
            session.Write(null, toWrite, out StatusCodeCollection results, out _);
            status = results[0];
        }
        catch (ServiceResultException exception)
        {
            status = exception.StatusCode;
        }

        if (writeEnabled)
        {
            Check("a permitted write reaches the source", StatusCode.IsGood(status), status.ToString());
            var read = ReadItem(session, "Writable.Setpoint");
            Check("the written value reads back", Equals(read.Value, 73.5d), read.Value?.ToString());
        }
        else
        {
            Check("write is refused when the adapter disables it",
                !StatusCode.IsGood(status), "the write was accepted");
        }
    }

    private static void CheckSubscription(Session session)
    {
        var subscription = new Subscription(session.DefaultSubscription)
        {
            PublishingInterval = 500,
            PublishingEnabled = true,
        };
        session.AddSubscription(subscription);
        subscription.Create();
        Check("a subscription is created", subscription.Created);

        var notifications = 0;
        foreach (var item in new[] { "Simulation.Ramp", "Simulation.Counter" })
        {
            var monitored = new MonitoredItem(subscription.DefaultItem)
            {
                StartNodeId = ItemNode(item),
                AttributeId = Attributes.Value,
                DisplayName = item,
            };
            monitored.Notification += (_, _) => Interlocked.Increment(ref notifications);
            subscription.AddItem(monitored);
        }
        subscription.ApplyChanges();
        Check("monitored items are created",
            subscription.MonitoredItems.All(item => StatusCode.IsGood(item.Status.Error?.StatusCode ?? StatusCodes.Good)),
            "a monitored item failed");

        // A Publish answered immediately turns a conforming client into a busy
        // loop and starves the sampling. Both show up here: the count has to
        // keep climbing over several seconds.
        Thread.Sleep(4000);
        var first = Volatile.Read(ref notifications);
        Check("changes arrive for the monitored items", first > 0, "no notification arrived");

        Thread.Sleep(4000);
        var second = Volatile.Read(ref notifications);
        Check("changes keep arriving while the subscription lives", second > first, $"{first} -> {second}");

        subscription.Delete(true);
        session.RemoveSubscription(subscription);
    }
}
