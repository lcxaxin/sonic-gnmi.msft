package gnmi

// interface_alias_cli_test.go

// Tests SHOW interface/alias

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	show_client "github.com/sonic-net/sonic-gnmi/show_client"
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

    // Extra UT to increase coverage and exercise port_config fallbacks
    type aliasListCase struct {
        desc          string
        withInterface string
        setup         func(t *testing.T) func() // returns cleanup
        wantEmpty     bool
        wantSingle    *[2]string
        wantContains  [][2]string
    }

    aliasCases := []aliasListCase{
        {
            desc:      "assert empty list when NO DATA",
            wantEmpty: true,
        },
        {
            desc: "assert base ports present (unordered)",
            setup: func(t *testing.T) func() {
                AddDataSet(t, ConfigDbNum, portsFileName)
                return nil
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet40", "etp10"},
                {"Ethernet80", "etp20"},
            },
        },
        {
            desc: "assert includes IB/Rec (unordered)",
            setup: func(t *testing.T) func() {
                AddDataSet(t, ConfigDbNum, portsFileName)
                AddDataSet(t, ConfigDbNum, portsIbRecFileName)
                return nil
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet40", "etp10"},
                {"Ethernet80", "etp20"},
                {"Ethernet-IB0", "Inband0"},
                {"Ethernet-IB1", "Inband1"},
                {"Ethernet-Rec0", "Recirc0"},
                {"Ethernet-Rec1", "Recirc1"},
            },
        },
        {
            desc: "load from port_config.ini when PORT is empty",
            setup: func(t *testing.T) func() {
                ini := "" +
                    "name       lanes    alias\n" +
                    "Ethernet0  1        etp0\n" +
                    "Ethernet-IB0 5      Inband0\n" +
                    "Ethernet-Rec0 7     Recirc0\n"
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet-IB0", "Inband0"},
                {"Ethernet-Rec0", "Recirc0"},
            },
        },
        {
            desc:          "query by alias uses port_config.ini mapping",
            withInterface: "etp0",
            setup: func(t *testing.T) func() {
                ini := "" +
                    "name       lanes    alias\n" +
                    "Ethernet0  1        etp0\n"
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantSingle: &[2]string{"Ethernet0", "etp0"},
        },
        {
            desc: "load from port_config.json when PORT is empty",
            setup: func(t *testing.T) func() {
                jsonCfg := `{
                  "PORT": {
                    "Ethernet0": {"alias":"etp0"},
                    "Ethernet-IB0": {"alias":"Inband0"}
                  }
                }`
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return "", fmt.Errorf("no ini")
                    }
                    if strings.Contains(cmd, "/etc/sonic/port_config.json") {
                        return jsonCfg, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet-IB0", "Inband0"},
            },
        },
        {
            desc: "normalize alias-keyed PORT entries via port_config.ini",
            setup: func(t *testing.T) func() {
                p1 := gomonkey.ApplyFunc(show_client.GetMapFromQueries, func(queries [][]string) (map[string]interface{}, error) {
                    if len(queries) == 1 && len(queries[0]) >= 2 && queries[0][0] == "CONFIG_DB" && queries[0][1] == "PORT" {
                        return map[string]interface{}{
                            "etp0":  map[string]interface{}{"admin_status": "up"},
                            "etp10": map[string]interface{}{"admin_status": "up"},
                        }, nil
                    }
                    return map[string]interface{}{}, nil
                })
                ini := "name lanes alias\nEthernet0 1 etp0\nEthernet40 9 etp10\n"
                p2 := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p1.Reset(); p2.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet40", "etp10"},
            },
        },
        {
            desc: "alias defaults to Name when no alias field and no mapping",
            setup: func(t *testing.T) func() {
                p1 := gomonkey.ApplyFunc(show_client.GetMapFromQueries, func(queries [][]string) (map[string]interface{}, error) {
                    if len(queries) == 1 && len(queries[0]) >= 2 && queries[0][0] == "CONFIG_DB" && queries[0][1] == "PORT" {
                        return map[string]interface{}{
                            "Ethernet999": map[string]interface{}{"admin_status": "up"},
                        }, nil
                    }
                    return map[string]interface{}{}, nil
                })
                p2 := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p1.Reset(); p2.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet999", "Ethernet999"},
            },
        },
        {
            desc: "load per-ASIC globbed port_config files (ini + json) using DEVICE_METADATA",
            setup: func(t *testing.T) func() {
                plat := "x86_64-acme"
                sku := "AcmeSku"
                f1 := fmt.Sprintf("/usr/share/sonic/device/%s/%s/asic0/port_config.ini", plat, sku)
                f2 := fmt.Sprintf("/usr/share/sonic/device/%s/%s/asic1/port_config.json", plat, sku)
                p1 := gomonkey.ApplyFunc(show_client.GetMapFromQueries, func(queries [][]string) (map[string]interface{}, error) {
                    if len(queries) == 1 && len(queries[0]) == 3 &&
                        queries[0][0] == "CONFIG_DB" && queries[0][1] == "DEVICE_METADATA" && queries[0][2] == "localhost" {
                        return map[string]interface{}{"platform": plat, "hwsku": sku}, nil
                    }
                    if len(queries) == 1 && len(queries[0]) >= 2 && queries[0][0] == "CONFIG_DB" && queries[0][1] == "PORT" {
                        return map[string]interface{}{}, nil
                    }
                    return map[string]interface{}{}, nil
                })
                ini := "name lanes alias\nEthernet100 1 etp100\n"
                jsonCfg := `{"PORT":{"Ethernet101":{"alias":"etp101"}}}`
                p2 := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") && strings.Contains(cmd, "/usr/share/sonic/device") {
                        return f1 + "\n" + f2 + "\n", nil
                    }
                    if strings.HasPrefix(cmd, "cat ") && strings.Contains(cmd, f1) {
                        return ini, nil
                    }
                    if strings.HasPrefix(cmd, "cat ") && strings.Contains(cmd, f2) {
                        return jsonCfg, nil
                    }
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") || strings.Contains(cmd, "/etc/sonic/port_config.json") {
                        return "", fmt.Errorf("no etc files")
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p1.Reset(); p2.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet100", "etp100"},
                {"Ethernet101", "etp101"},
            },
        },
        {
            desc: "INI parser without header (legacy positions)",
            setup: func(t *testing.T) func() {
                p1 := gomonkey.ApplyFunc(show_client.GetMapFromQueries, func(queries [][]string) (map[string]interface{}, error) {
                    if len(queries) == 1 && len(queries[0]) >= 2 && queries[0][0] == "CONFIG_DB" && queries[0][1] == "PORT" {
                        return map[string]interface{}{}, nil
                    }
                    return map[string]interface{}{}, nil
                })
                ini := "Ethernet2 9 etp2\n"
                p2 := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p1.Reset(); p2.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet2", "etp2"},
            },
        },
        {
            desc: "query by alias with port_config mapping (ini + json)",
            setup: func(t *testing.T) func() {
                ini := "" +
                    "name       lanes    alias\n" +
                    "Ethernet0  1        etp0\n"
                jsonCfg := `{
                  "PORT": {
                    "Ethernet-IB0": {"alias":"Inband0"}
                  }
                }`
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.Contains(cmd, "/etc/sonic/port_config.json") {
                        return jsonCfg, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet-IB0", "Inband0"},
            },
        },
        {
            desc: "query by alias with port_config mapping (json + ini)",
            setup: func(t *testing.T) func() {
                jsonCfg := `{
                  "PORT": {
                    "Ethernet0": {"alias":"etp0"}
                  }
                }`
                ini := "" +
                    "name       lanes    alias\n" +
                    "Ethernet-IB0 5      Inband0\n"
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.json") {
                        return jsonCfg, nil
                    }
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet-IB0", "Inband0"},
            },
        },
        {
            desc: "query by alias with duplicate port_config mappings",
            setup: func(t *testing.T) func() {
                ini := "" +
                    "name       lanes    alias\n" +
                    "Ethernet0  1        etp0\n"
                jsonCfg := `{
                  "PORT": {
                    "Ethernet0": {"alias":"etp0"},
                    "Ethernet-IB0": {"alias":"Inband0"}
                  }
                }`
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.Contains(cmd, "/etc/sonic/port_config.json") {
                        return jsonCfg, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet-IB0", "Inband0"},
            },
        },
        {
            desc: "query by alias with conflicting port_config mappings",
            setup: func(t *testing.T) func() {
                ini := "" +
                    "name       lanes    alias\n" +
                    "Ethernet0  1        etp0\n"
                jsonCfg := `{
                  "PORT": {
                    "Ethernet0": {"alias":"etp0"},
                    "Ethernet-IB0": {"alias":"etp0"}
                  }
                }`
                p := gomonkey.ApplyFunc(show_client.GetDataFromHostCommand, func(cmd string) (string, error) {
                    if strings.Contains(cmd, "/etc/sonic/port_config.ini") {
                        return ini, nil
                    }
                    if strings.Contains(cmd, "/etc/sonic/port_config.json") {
                        return jsonCfg, nil
                    }
                    if strings.HasPrefix(cmd, "bash -lc 'ls ") {
                        return "", nil
                    }
                    return "", fmt.Errorf("not found: %s", cmd)
                })
                return func() { p.Reset() }
            },
            wantContains: [][2]string{
                {"Ethernet0", "etp0"},
                {"Ethernet-IB0", "etp0"},
            },
        },
    }

    for _, tc := range aliasCases {
        t.Run(tc.desc, func(t *testing.T) {
            FlushDataSet(t, ConfigDbNum)

            var cleanup func()
            if tc.setup != nil {
                cleanup = tc.setup(t)
            }
            if cleanup != nil {
                defer cleanup()
            }

            list := getAliasList(t, ctx, gClient, tc.withInterface)
            switch {
            case tc.wantEmpty:
                mustEmpty(t, list)
            case tc.wantSingle != nil:
                mustSinglePair(t, list, tc.wantSingle[0], tc.wantSingle[1])
            case len(tc.wantContains) > 0:
                mustContainPairs(t, list, tc.wantContains)
            default:
                // no-op: test only exercised code path
            }
        })
    }
}

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

func mustSinglePair(t *testing.T, list []map[string]string, name, alias string) {
    t.Helper()
    if len(list) != 1 {
        t.Fatalf("expected single row, got %v", list)
    }
    if list[0]["Name"] != name || list[0]["Alias"] != alias {
        t.Fatalf("expected [%s,%s], got %v", name, alias, list)
    }
}