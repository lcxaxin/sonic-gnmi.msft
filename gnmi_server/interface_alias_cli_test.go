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

    aliasSingleEthernet0 := `[["Name","Alias"],["Ethernet0","etp0"]]`

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
        table := getAliasTable(t, ctx, gClient, "")
        mustHeaderOnly(t, table)
    })
    t.Run("assert base ports present (unordered)", func(t *testing.T) {
        FlushDataSet(t, ConfigDbNum)
        AddDataSet(t, ConfigDbNum, portsFileName)
        table := getAliasTable(t, ctx, gClient, "")
        mustContainPairs(t, table, [][2]string{
            {"Ethernet0", "etp0"},
            {"Ethernet40", "etp10"},
            {"Ethernet80", "etp20"},
        })
    })
    t.Run("assert includes IB/Rec (unordered)", func(t *testing.T) {
        FlushDataSet(t, ConfigDbNum)
        AddDataSet(t, ConfigDbNum, portsFileName)
        AddDataSet(t, ConfigDbNum, portsIbRecFileName)
        table := getAliasTable(t, ctx, gClient, "")
        mustContainPairs(t, table, [][2]string{
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

func getAliasTable(t *testing.T, ctx context.Context, client pb.GNMIClient, withInterface string) [][]string {
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
    var table [][]string
    if err := json.Unmarshal(raw, &table); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    return table
}

func mustContainPairs(t *testing.T, table [][]string, want [][2]string) {
    t.Helper()
    seen := map[[2]string]bool{}
    for _, row := range table[1:] {
        if len(row) == 2 {
            seen[[2]string{row[0], row[1]}] = true
        }
    }
    for _, p := range want {
        if !seen[p] {
            t.Fatalf("missing pair %v in table %v", p, table)
        }
    }
}

func mustHeaderOnly(t *testing.T, table [][]string) {
    t.Helper()
    if len(table) != 1 || len(table[0]) != 2 || table[0][0] != "Name" || table[0][1] != "Alias" {
        t.Fatalf("expected header-only, got %v", table)
    }
}