package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcua"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "address the UA endpoint listens on")
	browsable := flag.Bool("browse", true, "serve IOPCBrowseServerAddressSpace; -browse=false models a source that does not implement it")
	writeEnabled := flag.Bool("write-enabled", false, "allow Write to reach the source")
	readyFile := flag.String("ready-file", "", "file to write the listening address to once the endpoint is accepting connections")
	inventory := flag.Bool("inventory", false, "print the scripted ItemIDs and exit")
	flag.Parse()

	source := newScriptedSource(*browsable, *writeEnabled)

	if *inventory {
		for _, id := range source.sortedItemIDs() {
			fmt.Println(id)
		}
		return
	}

	// The endpoint URL has to name the address the client actually reaches,
	// and the port is only known after the socket is bound, so the listener is
	// opened first and the URL built from it.
	socket, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	endpointURL := fmt.Sprintf("opc.tcp://%s", socket.Addr().String())

	config := opcua.DefaultListenerConfig()
	config.Endpoint = opcua.EndpointConfig{
		EndpointURL:    endpointURL,
		ApplicationURI: "urn:opcda-access-adapter:uainterop",
		ProductURI:     "urn:opcda-access-adapter",
		// The harness is not the adapter and must not be mistaken for it in a
		// client's log or certificate store.
		ApplicationName: "OPC DA Access Adapter interop harness",
		// SecurityPolicy None is what an unencrypted interop run uses. It is
		// not a production configuration and ADR-0016 forbids describing it as
		// one.
		SecurityPolicyURI:   "http://opcfoundation.org/UA/SecurityPolicy#None",
		TransportProfileURI: "http://opcfoundation.org/UA-Profile/Transport/uatcp-uasc-uabinary",
	}
	config.AddressSpace = opcua.AddressSpaceConfig{
		NamespaceURI:     "urn:opcda-access-adapter:uainterop:source",
		SourceFolderName: "ScriptedSource",
		ManufacturerName: "opcda-access-adapter",
		SoftwareVersion:  "interop-harness",
	}
	config.DataAccess.RequestTimeout = 5 * time.Second
	config.DataAccess.MaxNodes = config.Population.MaxNodes
	config.Subscriptions.MaxNodes = config.Population.MaxNodes

	listener, err := opcua.NewListenerWithRuntime(config, source, 0x4A17_0001, 0x4A17_0002)
	if err != nil {
		log.Fatalf("create listener: %v", err)
	}

	if *readyFile != "" {
		if err := os.WriteFile(*readyFile, []byte(endpointURL+"\n"), 0o600); err != nil {
			log.Fatalf("write ready file: %v", err)
		}
	}
	log.Printf("interop harness listening on %s (browse=%v write=%v)", endpointURL, *browsable, *writeEnabled)

	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-served:
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := listener.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		if err := source.Shutdown(ctx); err != nil {
			log.Printf("source shutdown: %v", err)
		}
	}
}
