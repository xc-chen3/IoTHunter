package core

import "testing"

func TestNormalizeSerialPeripheralRecordProvidesUsableDefaults(t *testing.T) {
	peripheral := Peripheral{WorkspaceID: "W-1", Name: " UART ", Kind: "UART", Port: " /dev/ttyUSB0 "}
	normalizePeripheralRecord(&peripheral)
	if peripheral.Name != "UART" || peripheral.Kind != "serial" || peripheral.Port != "/dev/ttyUSB0" {
		t.Fatalf("identity was not normalized: %+v", peripheral)
	}
	if peripheral.Driver != "go.bug.st/serial" || peripheral.Transport != "usb-serial" {
		t.Fatalf("driver defaults missing: %+v", peripheral)
	}
	if peripheral.Config["baud_rate"] != 115200 || peripheral.Config["data_bits"] != 8 || peripheral.Config["parity"] != "none" || peripheral.Config["read_timeout_ms"] != 250 {
		t.Fatalf("serial defaults missing: %+v", peripheral.Config)
	}
	if len(peripheral.Capabilities) != 5 {
		t.Fatalf("serial capabilities missing: %+v", peripheral.Capabilities)
	}
}

func TestFindMatchingPeripheralIsScopedToWorkspaceAndEndpoint(t *testing.T) {
	items := []Peripheral{{ID: "PER-1", WorkspaceID: "W-1", Kind: "serial", Port: "/dev/ttyUSB0"}}
	if match, ok := findMatchingPeripheral(items, Peripheral{WorkspaceID: "W-1", Kind: "serial", Port: "/dev/ttyUSB0"}); !ok || match.ID != "PER-1" {
		t.Fatalf("duplicate endpoint was not found: %+v, %v", match, ok)
	}
	if _, ok := findMatchingPeripheral(items, Peripheral{WorkspaceID: "W-2", Kind: "serial", Port: "/dev/ttyUSB0"}); ok {
		t.Fatal("endpoint in another workspace was treated as a duplicate")
	}
}
