// Command capturehello captures and parses the TLS ClientHello produced by an
// arbitrary command, so fingerprints can be inspected without external tools.
//
// It listens on a loopback port, runs the given command with %s substituted by
// the listener URL (or appends the URL if no %s is present), reads the first
// ClientHello, and prints the cipher and extension order.
//
// Examples:
//
//	go run ./internal/capturehello 'curl -s --http2 -k -o /dev/null %s'
//	go run ./internal/capturehello --hex 'openssl s_client -connect %s:443'
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

func main() {
	hexOut := flag.Bool("hex", false, "also print the raw ClientHello as hex")
	timeoutSecs := flag.Int("timeout", 8, "seconds to wait for the connection")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: capturehello [flags] 'command template (optionally containing a percent-s placeholder)'")
		os.Exit(2)
	}
	template := strings.Join(args, " ")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	defer ln.Close()
	url := fmt.Sprintf("https://localhost:%d/", ln.Addr().(*net.TCPAddr).Port)

	raw := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			raw <- nil
			return
		}
		defer conn.Close()
		data, _ := readRecord(conn)
		raw <- data
	}()

	command := template
	if strings.Contains(template, "%s") {
		command = strings.ReplaceAll(template, "%s", url)
	} else {
		command = template + " " + url
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	data := <-raw
	if len(data) == 0 {
		fatal(fmt.Errorf("no ClientHello captured (is the command %q able to reach %s?)", command, url))
	}

	info, err := parseClientHello(data)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("ciphers : %s\n", joinHex(info.ciphers))
	fmt.Printf("exts    : %s\n", joinHex(info.extensions))
	fmt.Printf("exts_raw: %s\n", joinHex(info.extensionsClean))
	fmt.Printf("curves  : %s\n", joinHex(info.curves))
	fmt.Printf("sigalgs : %s\n", joinHex(info.sigalgs))
	if *hexOut {
		fmt.Printf("hex     : %x\n", data)
	}
	_ = timeoutSecs
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "capturehello:", err)
	os.Exit(1)
}

func joinHex(values []uint16) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, "-")
}

func readRecord(conn net.Conn) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for len(buf) < 5 {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, err
		}
	}
	length := int(buf[3])<<8 | int(buf[4])
	for len(buf) < 5+length {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

type helloInfo struct {
	ciphers         []uint16
	extensions      []uint16
	extensionsClean []uint16
	curves          []uint16
	sigalgs         []uint16
}

var grease = map[uint16]bool{
	0x0a0a: true, 0x1a1a: true, 0x2a2a: true, 0x3a3a: true,
	0x4a4a: true, 0x5a5a: true, 0x6a6a: true, 0x7a7a: true,
	0x8a8a: true, 0x9a9a: true, 0xaaaa: true, 0xbaba: true,
	0xcaca: true, 0xdada: true, 0xeaea: true, 0xfafa: true,
}

func parseClientHello(data []byte) (*helloInfo, error) {
	if len(data) < 9 || data[0] != 0x16 {
		return nil, fmt.Errorf("not a TLS handshake record")
	}
	body := data[5:]
	if body[0] != 0x01 {
		return nil, fmt.Errorf("not a ClientHello (type %d)", body[0])
	}
	p := 4 // handshake header
	p += 2 // legacy version
	p += 32
	if p >= len(body) {
		return nil, fmt.Errorf("truncated ClientHello")
	}
	sidLen := int(body[p])
	p += 1 + sidLen
	if p+2 > len(body) {
		return nil, fmt.Errorf("truncated cipher list")
	}
	csLen := int(body[p])<<8 | int(body[p+1])
	p += 2
	info := &helloInfo{}
	for i := 0; i+1 < csLen && p+i+1 < len(body); i += 2 {
		info.ciphers = append(info.ciphers, uint16(body[p+i])<<8|uint16(body[p+i+1]))
	}
	p += csLen
	if p >= len(body) {
		return info, nil
	}
	compLen := int(body[p])
	p += 1 + compLen
	if p+2 > len(body) {
		return info, nil
	}
	extTotal := int(body[p])<<8 | int(body[p+1])
	p += 2
	end := p + extTotal
	for p+4 <= end && p+4 <= len(body) {
		etype := uint16(body[p])<<8 | uint16(body[p+1])
		elen := int(body[p+2])<<8 | int(body[p+3])
		edata := body[p+4 : min(p+4+elen, len(body))]
		info.extensions = append(info.extensions, etype)
		switch etype {
		case 10:
			info.curves = parseUint16List(edata)
		case 13:
			info.sigalgs = parseUint16List(edata)
		}
		p += 4 + elen
	}
	for _, e := range info.extensions {
		if !grease[e] {
			info.extensionsClean = append(info.extensionsClean, e)
		}
	}
	return info, nil
}

// parseUint16List reads a length-prefixed list of uint16 values.
func parseUint16List(data []byte) []uint16 {
	if len(data) < 2 {
		return nil
	}
	n := int(data[0])<<8 | int(data[1])
	var out []uint16
	for i := 2; i+1 < len(data) && i < 2+n; i += 2 {
		out = append(out, uint16(data[i])<<8|uint16(data[i+1]))
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
