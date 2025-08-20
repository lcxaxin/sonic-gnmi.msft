package gnmi

// interface_alias_cli_test.go

// Tests SHOW interface/alias

import (
	"crypto/tls"
	"encoding/json"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

func TestGetShowInterfaceAlias(t *testing.T) {
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

    portsFileName := "../testdata/PORTS.txt"
    portsIbRecFileName := "../testdata/PORTS_IB_REC_ONLY.txt"

    aliasSingleEthernet0 := `[{"Name":"Ethernet0","Alias":"etp0"}]`

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
            desc:       "query SHOW interface alias NO DATA",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "alias" >
            `,
            wantRetCode: codes.OK,
            valTest:     false,
            testInit: func() {
                FlushDataSet(t, ConfigDbNum)
            },
        },
        {
            desc:       "query SHOW interface alias (load base ports)",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "alias" >
            `,
            wantRetCode: codes.OK,
            valTest:     false,
            testInit: func() {
                FlushDataSet(t, ConfigDbNum)
                AddDataSet(t, ConfigDbNum, portsFileName)
            },
        },
        {
            desc:       "query SHOW interface alias with interface option",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "alias" key: { key: "interface" value: "Ethernet0" } >
            `,
            wantRetCode: codes.OK,
            wantRespVal: []byte(aliasSingleEthernet0),
            valTest:     true,
        },
        {
            desc:       "query SHOW interface alias includes IB/Rec when present",
            pathTarget: "SHOW",
            textPbPath: `
                elem: <name: "interface" >
                elem: <name: "alias" >
            `,
            wantRetCode: codes.OK,
            valTest:     false,
            testInit: func() {
                AddDataSet(t, ConfigDbNum, portsFileName)
                AddDataSet(t, ConfigDbNum, portsIbRecFileName)
            },
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

    t.Run("assert header-only when NO DATA", func(t *testing.T) {
        FlushDataSet(t, ConfigDbNum)
        list := getAliasList(t, ctx, gClient, "")
		mustEmpty(t, list)
    })
    t.Run("assert base ports present (unordered)", func(t *testing.T) {
        FlushDataSet(t, ConfigDbNum)
        AddDataSet(t, ConfigDbNum, portsFileName)
        list := getAliasList(t, ctx, gClient, "")
        mustContainPairs(t, list, [][2]string{
            {"Ethernet0", "etp0"},
            {"Ethernet40", "etp10"},
            {"Ethernet80", "etp20"},
        })
    })
    t.Run("assert includes IB/Rec (unordered)", func(t *testing.T) {
        FlushDataSet(t, ConfigDbNum)
        AddDataSet(t, ConfigDbNum, portsFileName)
        AddDataSet(t, ConfigDbNum, portsIbRecFileName)
        list := getAliasList(t, ctx, gClient, "")
        mustContainPairs(t, list, [][2]string{
            {"Ethernet0", "etp0"},
            {"Ethernet40", "etp10"},
            {"Ethernet80", "etp20"},
            {"Ethernet-IB0", "Inband0"},
            {"Ethernet-IB1", "Inband1"},
            {"Ethernet-Rec0", "Recirc0"},
            {"Ethernet-Rec1", "Recirc1"},
        })
    })
}

// Replace helper to parse list of objects
func getAliasList(t *testing.T, ctx context.Context, client pb.GNMIClient, withInterface string) []map[string]string {
    t.Helper()
    elems := []*pb.PathElem{{Name: "interface"}}
    if withInterface != "" {
        elems = append(elems, &pb.PathElem{Name: "alias", Key: map[string]string{"interface": withInterface}})
    } else {
        elems = append(elems, &pb.PathElem{Name: "alias"})
    }
    req := &pb.GetRequest{
        Prefix:   &pb.Path{Target: "SHOW"},
        Path:     []*pb.Path{{Elem: elems}},
        Encoding: pb.Encoding_JSON_IETF,
    }
    resp, err := client.Get(ctx, req)
    if err != nil {
        t.Fatalf("Get failed: %v", err)
    }
    if len(resp.Notification) == 0 || len(resp.Notification[0].Update) == 0 {
        return nil
    }
    raw := resp.Notification[0].Update[0].Val.GetJsonIetfVal()
    var list []map[string]string
    if err := json.Unmarshal(raw, &list); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    return list
}

func mustEmpty(t *testing.T, list []map[string]string) {
    t.Helper()
    if len(list) != 0 {
        t.Fatalf("expected empty list, got %v", list)
    }
}

func mustContainPairs(t *testing.T, list []map[string]string, want [][2]string) {
    t.Helper()
    seen := map[[2]string]bool{}
    for _, row := range list {
        n, a := row["Name"], row["Alias"]
        seen[[2]string{n, a}] = true
    }
    for _, p := range want {
        if !seen[p] {
            t.Fatalf("missing pair %v in list %v", p, list)
        }
    }
}

func mustHeaderOnly(t *testing.T, table [][]string) {
    t.Helper()
    if len(table) != 1 || len(table[0]) != 2 || table[0][0] != "Name" || table[0][1] != "Alias" {
        t.Fatalf("expected header-only, got %v", table)
    }
}