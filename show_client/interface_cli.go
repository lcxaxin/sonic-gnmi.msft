package show_client

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

type InterfaceCountersResponse struct {
	State  string
	RxOk   string
	RxBps  string
	RxUtil string
	RxErr  string
	RxDrp  string
	RxOvr  string
	TxOk   string
	TxBps  string
	TxUtil string
	TxErr  string
	TxDrp  string
	TxOvr  string
}

func calculateByteRate(rate string) string {
	if rate == defaultMissingCounterValue {
		return defaultMissingCounterValue
	}
	rateFloatValue, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultMissingCounterValue
	}
	var formatted string
	switch {
	case rateFloatValue > 10*1e6:
		formatted = fmt.Sprintf("%.2f MB", rateFloatValue/1e6)
	case rateFloatValue > 10*1e3:
		formatted = fmt.Sprintf("%.2f KB", rateFloatValue/1e3)
	default:
		formatted = fmt.Sprintf("%.2f B", rateFloatValue)
	}

	return formatted + "/s"
}

func calculateUtil(rate string, portSpeed string) string {
	if rate == defaultMissingCounterValue || portSpeed == defaultMissingCounterValue {
		return defaultMissingCounterValue
	}
	byteRate, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultMissingCounterValue
	}
	portRate, err := strconv.ParseFloat(portSpeed, 64)
	if err != nil {
		return defaultMissingCounterValue
	}
	util := byteRate / (portRate * 1e6 / 8.0) * 100.0
	return fmt.Sprintf("%.2f%%", util)
}

func computeState(iface string, portTable map[string]interface{}) string {
	entry, ok := portTable[iface].(map[string]interface{})
	if !ok {
		return "X"
	}
	adminStatus := fmt.Sprint(entry["admin_status"])
	operStatus := fmt.Sprint(entry["oper_status"])

	switch {
	case adminStatus == "down":
		return "X"
	case adminStatus == "up" && operStatus == "up":
		return "U"
	case adminStatus == "up" && operStatus == "down":
		return "D"
	default:
		return "X"
	}
}

func getInterfaceCounters(options sdc.OptionMap) ([]byte, error) {
	var ifaces []string
	period := 0
	takeDiffSnapshot := false

	if interfaces, ok := options["interfaces"].Strings(); ok {
		ifaces = interfaces
	}

	if periodValue, ok := options["period"].Int(); ok {
		takeDiffSnapshot = true
		period = periodValue
	}

	if period > maxShowCommandPeriod {
		return nil, fmt.Errorf("period value must be <= %v", maxShowCommandPeriod)
	}

	oldSnapshot, err := getInterfaceCountersSnapshot(ifaces)
	if err != nil {
		log.Errorf("Unable to get interfaces counter snapshot due to err: %v", err)
		return nil, err
	}

	if !takeDiffSnapshot {
		return json.Marshal(oldSnapshot)
	}

	time.Sleep(time.Duration(period) * time.Second)

	newSnapshot, err := getInterfaceCountersSnapshot(ifaces)
	if err != nil {
		log.Errorf("Unable to get new interface counters snapshot due to err %v", err)
		return nil, err
	}

	// Compare diff between snapshot
	diffSnapshot := calculateDiffSnapshot(oldSnapshot, newSnapshot)

	return json.Marshal(diffSnapshot)
}

func getInterfaceCountersSnapshot(ifaces []string) (map[string]InterfaceCountersResponse, error) {
	queries := [][]string{
		{"COUNTERS_DB", "COUNTERS", "Ethernet*"},
	}

	aliasCountersOutput, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	portCounters := RemapAliasToPortName(aliasCountersOutput)

	queries = [][]string{
		{"COUNTERS_DB", "RATES", "Ethernet*"},
	}

	aliasRatesOutput, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	portRates := RemapAliasToPortName(aliasRatesOutput)

	queries = [][]string{
		{"APPL_DB", "PORT_TABLE"},
	}

	portTable, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	validatedIfaces := []string{}

	if len(ifaces) == 0 {
		for port, _ := range portCounters {
			validatedIfaces = append(validatedIfaces, port)
		}
	} else { // Validate
		for _, iface := range ifaces {
			_, found := portCounters[iface]
			if found { // Drop none valid interfaces
				validatedIfaces = append(validatedIfaces, iface)
			}
		}
	}

	response := make(map[string]InterfaceCountersResponse, len(ifaces))

	for _, iface := range validatedIfaces {
		state := computeState(iface, portTable)
		portSpeed := GetFieldValueString(portTable, iface, defaultMissingCounterValue, "speed")
		rxBps := GetFieldValueString(portRates, iface, defaultMissingCounterValue, "RX_BPS")
		txBps := GetFieldValueString(portRates, iface, defaultMissingCounterValue, "TX_BPS")

		response[iface] = InterfaceCountersResponse{
			State:  state,
			RxOk:   GetSumFields(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_IN_UCAST_PKTS", "SAI_PORT_STAT_IF_IN_NON_UCAST_PKTS"),
			RxBps:  calculateByteRate(rxBps),
			RxUtil: calculateUtil(rxBps, portSpeed),
			RxErr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_IN_ERRORS"),
			RxDrp:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_IN_DISCARDS"),
			RxOvr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_ETHER_RX_OVERSIZE_PKTS"),
			TxOk:   GetSumFields(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_OUT_UCAST_PKTS", "SAI_PORT_STAT_IF_OUT_NON_UCAST_PKTS"),
			TxBps:  calculateByteRate(txBps),
			TxUtil: calculateUtil(txBps, portSpeed),
			TxErr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_OUT_ERRORS"),
			TxDrp:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_OUT_DISCARDS"),
			TxOvr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_ETHER_TX_OVERSIZE_PKTS"),
		}
	}
	return response, nil
}

func calculateDiffSnapshot(oldSnapshot map[string]InterfaceCountersResponse, newSnapshot map[string]InterfaceCountersResponse) map[string]InterfaceCountersResponse {
	diffResponse := make(map[string]InterfaceCountersResponse, len(newSnapshot))

	for iface, newResp := range newSnapshot {
		oldResp, found := oldSnapshot[iface]
		if !found {
			oldResp = InterfaceCountersResponse{
				RxOk:  "0",
				RxErr: "0",
				RxDrp: "0",
				TxOk:  "0",
				TxErr: "0",
				TxDrp: "0",
				TxOvr: "0",
			}
		}
		diffResponse[iface] = InterfaceCountersResponse{
			State:  newResp.State,
			RxOk:   calculateDiffCounters(oldResp.RxOk, newResp.RxOk, defaultMissingCounterValue),
			RxBps:  newResp.RxBps,
			RxUtil: newResp.RxUtil,
			RxErr:  calculateDiffCounters(oldResp.RxErr, newResp.RxErr, defaultMissingCounterValue),
			RxDrp:  calculateDiffCounters(oldResp.RxDrp, newResp.RxDrp, defaultMissingCounterValue),
			RxOvr:  calculateDiffCounters(oldResp.RxOvr, newResp.RxOvr, defaultMissingCounterValue),
			TxOk:   calculateDiffCounters(oldResp.TxOk, newResp.TxOk, defaultMissingCounterValue),
			TxBps:  newResp.TxBps,
			TxUtil: newResp.TxUtil,
			TxErr:  calculateDiffCounters(oldResp.TxErr, newResp.TxErr, defaultMissingCounterValue),
			TxDrp:  calculateDiffCounters(oldResp.TxDrp, newResp.TxDrp, defaultMissingCounterValue),
			TxOvr:  calculateDiffCounters(oldResp.TxOvr, newResp.TxOvr, defaultMissingCounterValue),
		}
	}
	return diffResponse
}

var allPortErrors = [][]string{
	{"oper_error_status", "oper_error_status_time"},
	{"mac_local_fault_count", "mac_local_fault_time"},
	{"mac_remote_fault_count", "mac_remote_fault_time"},
	{"fec_sync_loss_count", "fec_sync_loss_time"},
	{"fec_alignment_loss_count", "fec_alignment_loss_time"},
	{"high_ser_error_count", "high_ser_error_time"},
	{"high_ber_error_count", "high_ber_error_time"},
	{"data_unit_crc_error_count", "data_unit_crc_error_time"},
	{"data_unit_misalignment_error_count", "data_unit_misalignment_error_time"},
	{"signal_local_error_count", "signal_local_error_time"},
	{"crc_rate_count", "crc_rate_time"},
	{"data_unit_size_count", "data_unit_size_time"},
	{"code_group_error_count", "code_group_error_time"},
	{"no_rx_reachability_count", "no_rx_reachability_time"},
}

func getInterfaceErrors(options sdc.OptionMap) ([]byte, error) {
	intf, ok := options["interface"].String()
	if !ok {
		return nil, fmt.Errorf("No interface name passed in as option")
	}

	// Query Port Operational Errors Table from STATE_DB
	queries := [][]string{
		{"STATE_DB", "PORT_OPERR_TABLE", intf},
	}
	portErrorsTbl, _ := GetMapFromQueries(queries)
	portErrorsTbl = RemapAliasToPortName(portErrorsTbl)

	// Format the port errors data
	portErrors := make([]map[string]string, 0, len(allPortErrors)+1)
	// Iterate through all port errors types and create the result
	for _, portError := range allPortErrors {
		count := "0"
		timestamp := "Never"
		if portErrorsTbl != nil {
			if val, ok := portErrorsTbl[portError[0]]; ok {
				count = fmt.Sprintf("%v", val)
			}
			if val, ok := portErrorsTbl[portError[1]]; ok {
				timestamp = fmt.Sprintf("%v", val)
			}
		}

		portErrors = append(portErrors, map[string]string{
			"Port Errors":         strings.Replace(strings.Replace(portError[0], "_", " ", -1), " count", "", -1),
			"Count":               count,
			"Last timestamp(UTC)": timestamp},
		)
	}

	// Convert [][]string to []byte using JSON serialization
	return json.Marshal(portErrors)
}

func getFrontPanelPorts(intf string) ([]string, error) {
	// Get the front panel ports from the SONiC CONFIG_DB
	queries := [][]string{
		{"CONFIG_DB", "PORT"},
	}
	frontPanelPorts, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Failed to get front panel ports: %v", err)
		return nil, err
	}

	// If intf is specified, return only that interface
	if intf != "" {
		if _, ok := frontPanelPorts[intf]; !ok {
			return nil, fmt.Errorf("interface %s not found in front panel ports", intf)
		}
		return []string{intf}, nil
	}

	// If no specific interface is requested, return all front panel ports
	ports := make([]string, 0, len(frontPanelPorts))
	for key := range frontPanelPorts {
		ports = append(ports, key)
	}
	return ports, nil
}

func getInterfaceFecStatus(options sdc.OptionMap) ([]byte, error) {
	intf, _ := options["interface"].String()

	ports, err := getFrontPanelPorts(intf)
	if err != nil {
		log.Errorf("Failed to get front panel ports: %v", err)
		return nil, err
	}
	ports = natsortInterfaces(ports)

	portFecStatus := make([]map[string]string, 0, len(ports)+1)
	for i := range ports {
		port := ports[i]
		adminFecStatus := ""
		operStatus := ""
		operFecStatus := ""

		// Query port admin FEC status and operation status from APPL_DB
		queries := [][]string{
			{"APPL_DB", AppDBPortTable, port},
		}
		data, err := GetMapFromQueries(queries)
		if err != nil {
			log.Errorf("Failed to get admin FEC status for port %s: %v", port, err)
			return nil, err
		}
		if _, ok := data["fec"]; !ok {
			adminFecStatus = "N/A"
		} else {
			adminFecStatus = fmt.Sprint(data["fec"])
		}
		if _, ok := data["oper_status"]; !ok {
			operStatus = "N/A"
		} else {
			operStatus = fmt.Sprint(data["oper_status"])
		}

		// Query port's oper FEC status from STATE_DB
		queries = [][]string{
			{"STATE_DB", StateDBPortTable, port},
		}
		data, err = GetMapFromQueries(queries)
		if err != nil {
			log.Errorf("Failed to get oper FEC status for port %s: %v", port, err)
			return nil, err
		}
		if _, ok := data["fec"]; !ok {
			operFecStatus = "N/A"
		} else {
			operFecStatus = fmt.Sprint(data["fec"])
		}

		if operStatus != "up" {
			// If port is down or oper FEC status is not available, set it to "N/A"
			operFecStatus = "N/A"
		}
		portFecStatus = append(portFecStatus, map[string]string{"Interface": port, "FEC Oper": operFecStatus, "FEC Admin": adminFecStatus})
	}

	return json.Marshal(portFecStatus)
}

func getInterfaceAlias(options sdc.OptionMap) ([]byte, error) {
    intf, _ := options["interface"].String()

    // Read CONFIG_DB.PORT
    queries := [][]string{{"CONFIG_DB", "PORT"}}
    portEntries, err := GetMapFromQueries(queries)
    if err != nil {
        log.Errorf("Failed to get ports from CONFIG_DB: %v", err)
        return nil, err
    }

    // Best-effort init alias maps
    _, _ = GetMapFromQueries([][]string{{"COUNTERS_DB", "RATES", "Ethernet*"}})

    // If map keys are alias (etp..), remap back to SONiC names
    portEntries = RemapAliasToPortName(portEntries)

    // Determine if alias field exists anywhere
    aliasFieldPresent := false
    for _, v := range portEntries {
        if entry, ok := v.(map[string]interface{}); ok {
            if _, ok := entry["alias"]; ok {
                aliasFieldPresent = true
                break
            }
        }
    }

    // Load name<->alias from port_config if needed
    name2alias, alias2name := map[string]string{}, map[string]string{}
    if !aliasFieldPresent {
        name2alias, alias2name = loadNameAliasMaps()
        // Normalize alias-keyed entries by our own map if needed
        if len(alias2name) > 0 {
            normalized := make(map[string]interface{}, len(portEntries))
            for k, v := range portEntries {
                if real, ok := alias2name[k]; ok {
                    normalized[real] = v
                } else {
                    normalized[k] = v
                }
            }
            portEntries = normalized
        }
    }

    // Build interface list (accept name or alias)
    interfaces := make([]string, 0)
    if intf != "" {
        if _, ok := portEntries[intf]; ok {
            interfaces = append(interfaces, intf)
        } else if real, ok := alias2name[intf]; ok {
            if _, ok := portEntries[real]; ok {
                interfaces = append(interfaces, real)
            } else {
                // Allow ports only defined in port_config (e.g., IB/Rec)
                if _, ok := name2alias[real]; ok {
                    interfaces = append(interfaces, real)
                }
            }
        } else if _, ok := name2alias[intf]; ok {
            // Name exists in port_config but not PORT table
            interfaces = append(interfaces, intf)
        }
        if len(interfaces) == 0 {
            return nil, fmt.Errorf("Invalid interface name %s ", intf)
        }
    } else {
        // Start with PORT table keys
        for port := range portEntries {
            interfaces = append(interfaces, port)
        }
        // Include ports present only in port_config (e.g., Ethernet-IB*, Ethernet-Rec*)
        if len(name2alias) > 0 {
            existing := make(map[string]struct{}, len(interfaces))
            for _, p := range interfaces {
                existing[p] = struct{}{}
            }
            for n := range name2alias {
                if _, ok := existing[n]; !ok {
                    interfaces = append(interfaces, n)
                }
            }
        }
        interfaces = natsortInterfaces(interfaces)
    }

    // Build []map[string]string instead of [][]string with header
    out := make([]map[string]string, 0, len(interfaces))
    for _, itf := range interfaces {
        alias := itf
        if entry, ok := portEntries[itf].(map[string]interface{}); ok {
            if a, ok := entry["alias"]; ok {
                alias = fmt.Sprint(a)
            } else if a2, ok := name2alias[itf]; ok && a2 != "" {
                alias = a2
            }
        } else if a2, ok := name2alias[itf]; ok && a2 != "" {
            alias = a2
        }
        out = append(out, map[string]string{
            "Name":  itf,
            "Alias": alias,
        })
    }
    return json.Marshal(out)
}

func loadNameAliasMaps() (map[string]string, map[string]string) {
    name2alias := map[string]string{}
    alias2name := map[string]string{}

    plat, sku := getPlatformAndHwsku()

    // Read /etc/sonic
    candidates := []string{
        "/etc/sonic/port_config.ini",
        "/etc/sonic/port_config.json",
    }
    if plat != "" && sku != "" {
        candidates = append(candidates,
            filepath.Join("/usr/share/sonic/device", plat, sku, "port_config.ini"),
            filepath.Join("/usr/share/sonic/device", plat, sku, "port_config.json"),
            filepath.Join("/usr/share/sonic/device", plat, "port_config.ini"),
            filepath.Join("/usr/share/sonic/device", plat, "port_config.json"),
        )
    }

    filesTried := 0
    filesHit := 0

    // Read explicit candidates (if they exist)
    for _, p := range candidates {
        if b, err := readHostFile(p); err == nil && len(b) > 0 {
            filesTried++
            filesHit++
            if strings.HasSuffix(strings.ToLower(p), ".json") {
                n2a, a2n := parsePortConfigJSON(b)
                mergeMaps(name2alias, alias2name, n2a, a2n)
            } else {
                n2a, a2n := parsePortConfigINI(b)
                mergeMaps(name2alias, alias2name, n2a, a2n)
            }
        }
    }

    // Read wildcard (including per-ASIC subdirectories) and merge all matching files
    if plat != "" && sku != "" {
        globs := []string{
            filepath.Join("/usr/share/sonic/device", plat, sku, "port_config*.ini"),
            filepath.Join("/usr/share/sonic/device", plat, sku, "port_config*.json"),
            filepath.Join("/usr/share/sonic/device", plat, sku, "*", "port_config*.ini"),
            filepath.Join("/usr/share/sonic/device", plat, sku, "*", "port_config*.json"),
        }
        for _, patt := range globs {
            m, _ := readAllHostFilesByGlob(patt)
            for file, b := range m {
                filesTried++
                filesHit++
                if strings.HasSuffix(strings.ToLower(file), ".json") {
                    n2a, a2n := parsePortConfigJSON(b)
                    mergeMaps(name2alias, alias2name, n2a, a2n)
                } else {
                    n2a, a2n := parsePortConfigINI(b)
                    mergeMaps(name2alias, alias2name, n2a, a2n)
                }
            }
        }
    }

    log.V(2).Infof("port_config aliases loaded: %d entries (files tried:%d, loaded:%d)", len(name2alias), filesTried, filesHit)
    return name2alias, alias2name
}

// INI (CSV) parser for port_config
func parsePortConfigINI(b []byte) (map[string]string, map[string]string) {
    name2alias := map[string]string{}
    alias2name := map[string]string{}

    lines := strings.Split(string(b), "\n")

    aliasIdx, nameIdx := -1, -1
    dataStart := -1
    for i, raw := range lines {
        line := strings.TrimSpace(raw)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        cols := strings.Fields(line) // split by whitespace
        if len(cols) < 3 {
            continue
        }

        // Try explicit header detection
        nameIdx, aliasIdx = -1, -1
        for j, c := range cols {
            lc := strings.ToLower(strings.TrimSpace(c))
            if lc == "name" {
                nameIdx = j
            }
            if lc == "alias" {
                aliasIdx = j
            }
        }
        if nameIdx >= 0 && aliasIdx >= 0 {
            dataStart = i + 1
        } else {
            // No header: treat this as first data row; use legacy positions
            nameIdx, aliasIdx = 0, 2
            dataStart = i
        }
        break
    }

    if nameIdx < 0 || aliasIdx < 0 || dataStart < 0 {
        return name2alias, alias2name
    }

    for _, raw := range lines[dataStart:] {
        line := strings.TrimSpace(raw)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        cols := strings.Fields(line)
        if len(cols) <= aliasIdx || len(cols) <= nameIdx {
            continue
        }
        n := strings.TrimSpace(cols[nameIdx])
        a := strings.TrimSpace(cols[aliasIdx])
        if n != "" && a != "" {
            name2alias[n] = a
            alias2name[a] = n
        }
    }
    return name2alias, alias2name
}

// JSON parser for port_config
func parsePortConfigJSON(b []byte) (map[string]string, map[string]string) {
    name2alias := map[string]string{}
    alias2name := map[string]string{}

    var j map[string]map[string]map[string]interface{}
    if err := json.Unmarshal(b, &j); err != nil {
        return name2alias, alias2name
    }
    if ports, ok := j["PORT"]; ok {
        for n, attrs := range ports {
            if aRaw, ok := attrs["alias"]; ok {
                a := fmt.Sprint(aRaw)
                if n != "" && a != "" {
                    name2alias[n] = a
                    alias2name[a] = n
                }
            }
        }
    }
    return name2alias, alias2name
}

// helper: read platform/hwsku from CONFIG_DB
func getPlatformAndHwsku() (string, string) {
    md, err := GetMapFromQueries([][]string{{"CONFIG_DB", "DEVICE_METADATA", "localhost"}})
    if err != nil {
        return "", ""
    }
    plat := fmt.Sprint(md["platform"])
    hwsku := fmt.Sprint(md["hwsku"])
    return plat, hwsku
}

func readHostFile(path string) ([]byte, error) {
    out, err := GetDataFromHostCommand(fmt.Sprintf("cat %q", path))
    if err != nil {
        return nil, err
    }
    out = strings.TrimSuffix(out, "\n")
    if out == "" {
        return nil, fmt.Errorf("empty host file: %s", path)
    }
    return []byte(out), nil
}

func readAllHostFilesByGlob(pattern string) (map[string][]byte, error) {
    res := make(map[string][]byte)

    cmd := fmt.Sprintf("bash -lc 'ls -1 %s 2>/dev/null'", pattern)
    f, err := GetDataFromHostCommand(cmd)
    if err != nil {
        return res, err
    }
    for _, line := range strings.Split(strings.TrimSpace(f), "\n") {
        file := strings.TrimSpace(line)
        if file == "" {
            continue
        }
        if b, err := readHostFile(file); err == nil && len(b) > 0 {
            res[file] = b
        }
    }
    return res, nil
}

func mergeMaps(dstN2A, dstA2N, srcN2A, srcA2N map[string]string) {
    for k, v := range srcN2A {
        dstN2A[k] = v
    }
    for k, v := range srcA2N {
        dstA2N[k] = v
    }
}