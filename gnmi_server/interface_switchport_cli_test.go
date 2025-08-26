package gnmi

// interface_switchport_cli_test.go

// Tests SHOW interface/switchport/config and SHOW interface/switchport/status

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

// Test SHOW interface switchport config
func TestGetShowInterfaceSwitchportConfig(t *testing.T) {
    s := createServer(t, ServerPort)
    go runServer(t, s)
    defer s.ForceStop()
    defer ResetDataSetsAndMappings(t)

    tlsConfig := &tls.Config{InsecureSkipVerify: true}
    opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

    conn, err := grpc.Dial(TargetAddr, opts...)
    if err != nil {
        t.Fatalf("Dialing to %q failed: %v", TargetAddr, err)
    }
    defer conn.Close()

    gClient := pb.NewGNMIClient(conn)
    ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout*time.Second)
    defer cancel()

    portsFileName := "../testdata/PORTS_SWITCHPORT.txt"
    vlanMemberFileName := "../testdata/VLAN_MEMBER_SWITCHPORT.txt"

    expectedConfig := `[{"Interface":"Ethernet0","Mode":"trunk","Tagged":"","Untagged":"1000"},{"Interface":"Ethernet1","Mode":"trunk","Tagged":"","Untagged":"1000"},{"Interface":"Ethernet2","Mode":"trunk","Tagged":"","Untagged":"1000"},{"Interface":"Ethernet3","Mode":"trunk","Tagged":"","Untagged":"1000"},{"Interface":"Ethernet4","Mode":"trunk","Tagged":"","Untagged":"1000"},{"Interface":"Ethernet5","Mode":"trunk","Tagged":"","Untagged":"1000"},{"Interface":"Ethernet6","Mode":"routed","Tagged":"","Untagged":""},{"Interface":"Ethernet7","Mode":"routed","Tagged":"","Untagged":""}]`

    tests := []struct {
        desc        string
        pathTarget  string
        textPbPath  string
        wantRetCode codes.Code
        wantRespVal interface{}
        valTest     bool
        mockSleep   bool
        testInit    func()
    }{
        {
            desc:       "query SHOW interface switchport config NO DATA",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "switchport" >
                elem: <name: "config" >
            `,
            wantRetCode: codes.OK,
            valTest:     false,
            testInit: func() {
                FlushDataSet(t, ConfigDbNum)
            },
        },
        {
            desc:       "query SHOW interface switchport config (load ports + vlan_member)",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "switchport" >
                elem: <name: "config" >
            `,
            wantRetCode: codes.OK,
            wantRespVal: []byte(expectedConfig),
            valTest:     true,
            testInit: func() {
                FlushDataSet(t, ConfigDbNum)
                AddDataSet(t, ConfigDbNum, portsFileName)
                AddDataSet(t, ConfigDbNum, vlanMemberFileName)
            },
        },
        {
            desc:       "query SHOW interface switchport config with interface option (by name)",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "switchport" >
                elem: <name: "config" key: { key: "interface" value: "Ethernet0" } >
            `,
            wantRetCode: codes.OK,
            wantRespVal: []byte(`[{"Interface":"Ethernet0","Mode":"trunk","Tagged":"","Untagged":"1000"}]`),
            valTest:     true,
            // reuse dataset loaded by previous test (no testInit)
        },
    }

    for _, test := range tests {
        if test.testInit != nil {
            test.testInit()
        }
        var patches *gomonkey.Patches
        if test.mockSleep {
            patches = gomonkey.ApplyFunc(time.Sleep, func(d time.Duration) {})
        }

        t.Run(test.desc, func(t *testing.T) {
            runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
        })
        if patches != nil {
            patches.Reset()
        }
    }
}

// Test SHOW interface switchport status
func TestGetShowInterfaceSwitchportStatus(t *testing.T) {
    s := createServer(t, ServerPort)
    go runServer(t, s)
    defer s.ForceStop()
    defer ResetDataSetsAndMappings(t)

    tlsConfig := &tls.Config{InsecureSkipVerify: true}
    opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

    conn, err := grpc.Dial(TargetAddr, opts...)
    if err != nil {
        t.Fatalf("Dialing to %q failed: %v", TargetAddr, err)
    }
    defer conn.Close()

    gClient := pb.NewGNMIClient(conn)
    ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout*time.Second)
    defer cancel()

    portsFileName := "../testdata/PORTS_SWITCHPORT.txt"
    vlanMemberFileName := "../testdata/VLAN_MEMBER_SWITCHPORT.txt"

    expectedStatus := `[{"Interface":"Ethernet0","Mode":"trunk"},{"Interface":"Ethernet1","Mode":"trunk"},{"Interface":"Ethernet2","Mode":"trunk"},{"Interface":"Ethernet3","Mode":"trunk"},{"Interface":"Ethernet4","Mode":"trunk"},{"Interface":"Ethernet5","Mode":"trunk"},{"Interface":"Ethernet6","Mode":"routed"},{"Interface":"Ethernet7","Mode":"routed"}]`

    tests := []struct {
        desc        string
        pathTarget  string
        textPbPath  string
        wantRetCode codes.Code
        wantRespVal interface{}
        valTest     bool
        mockSleep   bool
        testInit    func()
    }{
        {
            desc:       "query SHOW interface switchport status NO DATA",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "switchport" >
                elem: <name: "status" >
            `,
            wantRetCode: codes.OK,
            valTest:     false,
            testInit: func() {
                FlushDataSet(t, ConfigDbNum)
            },
        },
        {
            desc:       "query SHOW interface switchport status (load ports + vlan_member)",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "switchport" >
                elem: <name: "status" >
            `,
            wantRetCode: codes.OK,
            wantRespVal: []byte(expectedStatus),
            valTest:     true,
            testInit: func() {
                FlushDataSet(t, ConfigDbNum)
                AddDataSet(t, ConfigDbNum, portsFileName)
                AddDataSet(t, ConfigDbNum, vlanMemberFileName)
            },
        },
        {
            desc:       "query SHOW interface switchport status with interface option (by name)",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "switchport" >
                elem: <name: "status" key: { key: "interface" value: "Ethernet0" } >
            `,
            wantRetCode: codes.OK,
            wantRespVal: []byte(`[{"Interface":"Ethernet0","Mode":"trunk"}]`),
            valTest:     true,
        },
    }

    for _, test := range tests {
        if test.testInit != nil {
            test.testInit()
        }
        var patches *gomonkey.Patches
        if test.mockSleep {
            patches = gomonkey.ApplyFunc(time.Sleep, func(d time.Duration) {})
        }

        t.Run(test.desc, func(t *testing.T) {
            runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
        })
        if patches != nil {
            patches.Reset()
        }
    }
}