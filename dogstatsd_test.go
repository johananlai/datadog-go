// Copyright 2013 Ooyala, Inc.

package dogstatsd

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
)

var dogstatsdTests = []struct {
	GlobalNamespace string
	GlobalTags      []string
	Method          string
	Metric          string
	Value           interface{}
	Tags            []string
	Rate            float64
	Expected        string
}{
	{"", nil, "Gauge", "test.gauge", 1.0, nil, 1.0, "test.gauge:1.000000|g"},
	{"", nil, "Gauge", "test.gauge", 1.0, nil, 0.999999, "test.gauge:1.000000|g|@0.999999"},
	{"", nil, "Gauge", "test.gauge", 1.0, []string{"tagA"}, 1.0, "test.gauge:1.000000|g|#tagA"},
	{"", nil, "Gauge", "test.gauge", 1.0, []string{"tagA", "tagB"}, 1.0, "test.gauge:1.000000|g|#tagA,tagB"},
	{"", nil, "Gauge", "test.gauge", 1.0, []string{"tagA"}, 0.999999, "test.gauge:1.000000|g|@0.999999|#tagA"},
	{"", nil, "Count", "test.count", int64(1), []string{"tagA"}, 1.0, "test.count:1|c|#tagA"},
	{"", nil, "Count", "test.count", int64(-1), []string{"tagA"}, 1.0, "test.count:-1|c|#tagA"},
	{"", nil, "Histogram", "test.histogram", 2.3, []string{"tagA"}, 1.0, "test.histogram:2.300000|h|#tagA"},
	{"", nil, "Set", "test.set", "uuid", []string{"tagA"}, 1.0, "test.set:uuid|s|#tagA"},
	{"flubber.", nil, "Set", "test.set", "uuid", []string{"tagA"}, 1.0, "flubber.test.set:uuid|s|#tagA"},
	{"", []string{"tagC"}, "Set", "test.set", "uuid", []string{"tagA"}, 1.0, "test.set:uuid|s|#tagC,tagA"},
}

func assertNotPanics(t *testing.T, f func()) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatal(r)
		}
	}()
	f()
}

func TestClient(t *testing.T) {
	addr := "localhost:1201"
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}

	server, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client, err := New(addr)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range dogstatsdTests {
		client.Namespace = tt.GlobalNamespace
		client.Tags = tt.GlobalTags
		method := reflect.ValueOf(client).MethodByName(tt.Method)
		e := method.Call([]reflect.Value{
			reflect.ValueOf(tt.Metric),
			reflect.ValueOf(tt.Value),
			reflect.ValueOf(tt.Tags),
			reflect.ValueOf(tt.Rate)})[0]
		errInter := e.Interface()
		if errInter != nil {
			t.Fatal(errInter.(error))
		}

		bytes := make([]byte, 1024)
		n, err := server.Read(bytes)
		if err != nil {
			t.Fatal(err)
		}
		message := bytes[:n]
		if string(message) != tt.Expected {
			t.Errorf("Expected: %s. Actual: %s", tt.Expected, string(message))
		}
	}
}

func TestBufferedClient(t *testing.T) {
	addr := "localhost:1201"
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}

	server, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}

	bufferLength := 5
	client := &Client{
		conn:         conn,
		commands:     make([]string, 0, bufferLength),
		bufferLength: bufferLength,
	}

	client.Namespace = "foo."
	client.Tags = []string{"dd:2"}

	client.Count("cc", 1, nil, 1)
	client.Gauge("gg", 10, nil, 1)
	client.Histogram("hh", 1, nil, 1)
	client.Set("ss", "ss", nil, 1)

	if len(client.commands) != 4 {
		t.Errorf("Expected client to have buffered 4 commands, but found %d\n", len(client.commands))
	}

	client.Set("ss", "xx", nil, 1)
	err = client.flush()
	if err != nil {
		t.Errorf("Error sending: %s", err)
	}

	if len(client.commands) != 0 {
		t.Errorf("Expecting send to flush commands, but found %d\n", len(client.commands))
	}

	buffer := make([]byte, 4096)
	n, err := server.Read(buffer)
	result := string(buffer[:n])

	if err != nil {
		t.Error(err)
	}

	expected := []string{
		`foo.cc:1|c|#dd:2`,
		`foo.gg:10.000000|g|#dd:2`,
		`foo.hh:1.000000|h|#dd:2`,
		`foo.ss:ss|s|#dd:2`,
		`foo.ss:xx|s|#dd:2`,
	}

	for i, res := range strings.Split(result, "\n") {
		if res != expected[i] {
			t.Errorf("Got `%s`, expected `%s`", res, expected[i])
		}
	}

}

func TestNilSafe(t *testing.T) {
	var c *Client = nil
	assertNotPanics(t, func() { c.Close() })
	assertNotPanics(t, func() { c.Count("", 0, nil, 1) })
	assertNotPanics(t, func() { c.Histogram("", 0, nil, 1) })
	assertNotPanics(t, func() { c.Gauge("", 0, nil, 1) })
	assertNotPanics(t, func() { c.Set("", "", nil, 1) })
	assertNotPanics(t, func() { c.send("", "", nil, 1) })
}

// These benchmarks show that using a buffer instead of sprintf-ing together
// a bunch of intermediate strings is 4-5x faster

func BenchmarkFormatNew(b *testing.B) {
	b.StopTimer()
	c := &Client{}
	c.Namespace = "foo.bar."
	c.Tags = []string{"app:foo", "host:bar"}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		c.format("system.cpu.idle", "10", []string{"foo"}, 1)
		c.format("system.cpu.load", "0.1", nil, 0.9)
	}
}

// Old formatting function, added to client for tests
func (c *Client) format_old(name, value string, tags []string, rate float64) string {
	if rate < 1 {
		value = fmt.Sprintf("%s|@%f", value, rate)
	}
	if c.Namespace != "" {
		name = fmt.Sprintf("%s%s", c.Namespace, name)
	}

	tags = append(c.Tags, tags...)
	if len(tags) > 0 {
		value = fmt.Sprintf("%s|#%s", value, strings.Join(tags, ","))
	}

	return fmt.Sprintf("%s:%s", name, value)

}

func BenchmarkFormatOld(b *testing.B) {
	b.StopTimer()
	c := &Client{}
	c.Namespace = "foo.bar."
	c.Tags = []string{"app:foo", "host:bar"}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		c.format_old("system.cpu.idle", "10", []string{"foo"}, 1)
		c.format_old("system.cpu.load", "0.1", nil, 0.9)
	}
}
