/*
	ORBITAL COMMAND - NODE ALPHA
	----------------------------------------
	A simple TCP server that simulates a command interface for a fictional "Orbital Command" system.
	Clients can connect and issue commands to interact with the server's file system and retrieve system information.

*/

package main

import (
	"bufio"
	"flag"
	"fmt"
	"hash/crc32"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PORT is the only true constant — everything else depends on color mode.
const PORT = ":9078"

// ANSI color vars — mutable so -no-color can zero them at startup.
var (
	ANSIReset  = "\033[0m"
	ANSIRed    = "\033[31m"
	ANSIGreen  = "\033[32m"
	ANSIYellow = "\033[33m"
	ANSICyan   = "\033[36m"
	ANSIBold   = "\033[1m"

	// PROMPT is rebuilt after flag parsing (depends on ANSI vars).
	PROMPT string
)

// --- COLOR HELPERS ---
// All color output flows through these. Zeroing ANSI vars disables color globally.

func colorOK() string                { return ANSIGreen + "OK" + ANSIReset }
func colorErr(msg string) string     { return ANSIRed + msg + ANSIReset }
func colorHeader(msg string) string  { return ANSICyan + msg + ANSIReset }
func colorWarn(msg string) string    { return ANSIYellow + msg + ANSIReset }
func colorBold(msg string) string    { return ANSIBold + msg + ANSIReset }
func colorDirName(name string) string { return ANSIBold + ANSICyan + name + ANSIReset }

// sep is the standard horizontal rule used in table output.
func sep() string { return colorHeader("---------------------------------------------------") }

// --- PACKAGE-LEVEL STATE ---

var (
	serverStart = time.Now()
	nickPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
	nodeName    string
)

var nodeWords = []string{
	"ALPHA", "BETA", "GAMMA", "DELTA", "ECHO",
	"FOXTROT", "GHOST", "HUNTER", "ION", "JADE",
	"KILO", "LIMA", "NOVA", "OSCAR", "PAPA",
	"ROMEO", "SIERRA", "TANGO", "VECTOR", "XRAY",
	"YANKEE", "ZULU",
}

func generateNodeName() string {
	return fmt.Sprintf("%s%04d", nodeWords[rand.Intn(len(nodeWords))], rand.Intn(10000))
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func centerPad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	total := width - len(s)
	left := total / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", total-left)
}

// --- CLIENT REGISTRY ---

type ClientInfo struct {
	Addr        string
	Nick        string
	ConnectedAt time.Time
}

func (c *ClientInfo) DisplayName() string {
	if c.Nick != "" {
		return c.Nick
	}
	return c.Addr
}

var clients = struct {
	mu sync.RWMutex
	m  map[net.Conn]*ClientInfo
}{m: make(map[net.Conn]*ClientInfo)}

func registerClient(conn net.Conn, addr string) {
	clients.mu.Lock()
	clients.m[conn] = &ClientInfo{Addr: addr, ConnectedAt: time.Now()}
	clients.mu.Unlock()
}

func unregisterClient(conn net.Conn) {
	clients.mu.Lock()
	delete(clients.m, conn)
	clients.mu.Unlock()
}

func broadcastMessage(sender net.Conn, senderName string, message string) int {
	clients.mu.RLock()
	defer clients.mu.RUnlock()
	count := 0
	for conn := range clients.m {
		if conn == sender {
			continue
		}
		conn.Write([]byte(colorWarn(fmt.Sprintf("\n[BROADCAST FROM %s]: %s", senderName, message)) + "\n" + PROMPT))
		count++
	}
	return count
}

func whisperClient(sender net.Conn, senderName string, target string, message string) bool {
	clients.mu.RLock()
	defer clients.mu.RUnlock()
	for conn, info := range clients.m {
		if conn == sender {
			continue
		}
		if strings.EqualFold(info.Nick, target) || info.Addr == target {
			conn.Write([]byte(colorWarn(fmt.Sprintf("\n[WHISPER FROM %s]: %s", senderName, message)) + "\n" + PROMPT))
			return true
		}
	}
	return false
}

// --- FILE ENTRY ---

type FileEntry struct {
	Name  string
	IsDir bool
	Size  int64
}

// --- MAIN ---

func main() {
	challengeMode := flag.Bool("c", false, "Require auth challenge before allowing access")
	rootDir := flag.String("d", "", "Root directory for the server (defaults to current directory)")
	nodeFlag := flag.String("n", "", "Node name shown in banner (default: randomly generated)")
	noColor := flag.Bool("no-color", false, "Disable ANSI color output")
	flag.Parse()

	// Zero ANSI vars if -no-color or NO_COLOR env is set.
	if *noColor || os.Getenv("NO_COLOR") != "" {
		ANSIReset = ""
		ANSIRed = ""
		ANSIGreen = ""
		ANSIYellow = ""
		ANSICyan = ""
		ANSIBold = ""
	}

	// Build PROMPT now that ANSI vars are final.
	PROMPT = ANSIBold + ANSIGreen + "READY > " + ANSIReset

	if *nodeFlag != "" {
		nodeName = strings.ToUpper(*nodeFlag)
	} else {
		nodeName = generateNodeName()
	}

	if *rootDir != "" {
		if err := os.Chdir(*rootDir); err != nil {
			fmt.Printf("[CRITICAL FAILURE] CANNOT SET ROOT DIRECTORY: %v\n", err)
			os.Exit(1)
		}
	}

	cwd, _ := os.Getwd()

	listener, err := net.Listen("tcp", PORT)
	if err != nil {
		fmt.Printf("[CRITICAL FAILURE] FLIGHT DECK UNRESPONSIVE: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	authStatus := "DISABLED"
	if *challengeMode {
		authStatus = "ENABLED"
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║      ORBITAL COMMAND  ::  SYSTEM ONLINE  ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║  NODE           : %-23s║\n", nodeName)
	fmt.Printf("║  PORT           : %-23s║\n", PORT)
	fmt.Printf("║  ROOT DIR       : %-23s║\n", truncateStr(cwd, 23))
	fmt.Printf("║  AUTH CHALLENGE : %-23s║\n", authStatus)
	if *noColor || os.Getenv("NO_COLOR") != "" {
		fmt.Println("║  ANSI COLOR     : DISABLED               ║")
	}
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Println("║  FLAGS:  -n <name>  -d <dir>  -c         ║")
	fmt.Println("║  Run with -h to see all options.         ║")
	fmt.Println("╚══════════════════════════════════════════╝")

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("[ERROR] DOCKING FAILED: %v\n", err)
			continue
		}
		go handleConnection(conn, *challengeMode)
	}
}

// --- CONNECTION HANDLER ---

func handleConnection(conn net.Conn, challengeMode bool) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	fmt.Printf("[NEW LINK] UPLINK ESTABLISHED FROM %s\n", clientAddr)

	title := "ORBITAL COMMAND - NODE " + nodeName
	banner := colorHeader("\n=========================================\n"+
		centerPad(title, 41)+"\n"+
		"=========================================\n") +
		"[UPLINK ESTABLISHED]\n" +
		"[SYSTEM ONLINE. AWAITING INPUT.]\n"

	conn.Write([]byte(banner))

	scanner := bufio.NewScanner(conn)

	if challengeMode {
		if !RunAuthChallenge(conn, scanner) {
			return
		}
		conn.Write([]byte("\n"))
	}

	registerClient(conn, clientAddr)
	defer unregisterClient(conn)

	sendMOTD(conn)
	conn.Write([]byte(PROMPT))

	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())

		if text == "" {
			conn.Write([]byte(PROMPT))
			continue
		}

		fmt.Printf("[%s] CMD: %s\n", clientAddr, text)

		parts := strings.Fields(text)
		cmd := strings.ToUpper(parts[0])
		args := parts[1:]

		switch cmd {
		case "PING":
			conn.Write([]byte(colorOK() + "\nPONG\n[LATENCY: <1ms]\n"))

		case "REPORT":
			conn.Write([]byte(getSystemReport()))

		case "TOUCH":
			if len(args) < 1 {
				conn.Write([]byte(colorErr("[ERROR: MISSING ARGUMENT]") + "\nFAIL\n"))
			} else {
				handleTouch(conn, args[0])
			}

		case "LIST":
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			handleList(conn, path)

		case "CAT":
			if len(args) < 1 {
				conn.Write([]byte(colorErr("[ERROR: MISSING ARGUMENT]") + "\nFAIL\n"))
			} else {
				handleCat(conn, args[0])
			}

		case "WRITE":
			if len(args) < 2 {
				conn.Write([]byte(colorErr("[ERROR: SYNTAX INVALID]\n[USAGE: WRITE <PATH> <CONTENT>]") + "\nFAIL\n"))
			} else {
				handleWrite(conn, args[0], strings.Join(args[1:], " "))
			}

		case "WRITEML":
			if len(args) < 2 {
				conn.Write([]byte(colorErr("[ERROR: SYNTAX INVALID]\n[USAGE: WRITEML <PATH> <DELIMITER>]") + "\nFAIL\n"))
			} else {
				handleWriteML(conn, scanner, args[0], args[1])
			}

		case "APPEND":
			if len(args) < 2 {
				conn.Write([]byte(colorErr("[ERROR: SYNTAX INVALID]\n[USAGE: APPEND <PATH> <CONTENT>]") + "\nFAIL\n"))
			} else {
				handleAppend(conn, args[0], strings.Join(args[1:], " "))
			}

		case "TREE":
			path := "."
			depth := 3
			if len(args) > 0 {
				path = args[0]
			}
			if len(args) > 1 {
				if d, err := strconv.Atoi(args[1]); err == nil && d >= 1 && d <= 10 {
					depth = d
				} else {
					conn.Write([]byte(colorErr("[ERROR: DEPTH MUST BE 1-10]") + "\nFAIL\n"))
					conn.Write([]byte(PROMPT))
					continue
				}
			}
			handleTree(conn, path, depth)

		case "NICK":
			if len(args) < 1 {
				conn.Write([]byte(colorErr("[ERROR: MISSING ARGUMENT]\n[USAGE: NICK <CALLSIGN>]") + "\nFAIL\n"))
			} else {
				handleNick(conn, args[0])
			}

		case "WHO":
			handleWho(conn)

		case "WHISPER":
			if len(args) < 2 {
				conn.Write([]byte(colorErr("[ERROR: SYNTAX INVALID]\n[USAGE: WHISPER <TARGET> <MESSAGE>]") + "\nFAIL\n"))
			} else {
				clients.mu.RLock()
				info := clients.m[conn]
				clients.mu.RUnlock()
				target := args[0]
				if strings.EqualFold(info.Nick, target) || info.Addr == target {
					conn.Write([]byte(colorErr("[ERROR: CANNOT WHISPER TO SELF]") + "\nFAIL\n"))
				} else if !whisperClient(conn, info.DisplayName(), target, strings.Join(args[1:], " ")) {
					conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: TARGET NOT FOUND: %s]", target)) + "\nFAIL\n"))
				} else {
					conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[WHISPER DELIVERED TO: %s]\n", target)))
				}
			}

		case "CHALLENGE":
			if !RunAuthChallenge(conn, scanner) {
				return
			}
			conn.Write([]byte("\n"))

		case "BROADCAST":
			if len(args) < 1 {
				conn.Write([]byte(colorErr("[ERROR: MISSING ARGUMENT]\n[USAGE: BROADCAST <MESSAGE>]") + "\nFAIL\n"))
			} else {
				clients.mu.RLock()
				info := clients.m[conn]
				clients.mu.RUnlock()
				n := broadcastMessage(conn, info.DisplayName(), strings.Join(args, " "))
				conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[BROADCAST SENT TO: %d NODES]\n", n)))
			}

		case "HELP":
			handleHelp(conn)

		case "EXIT", "QUIT":
			conn.Write([]byte("[CLOSING UPLINK...]\n[GOODBYE COMMANDER]\n"))
			return

		default:
			conn.Write([]byte(colorErr(fmt.Sprintf("[ERR UNKNOWN COMMAND: %s]\n[TRY 'HELP']", cmd)) + "\nFAIL\n"))
		}

		conn.Write([]byte(PROMPT))
	}
}

// --- COMMAND IMPLEMENTATIONS ---

func handleHelp(conn net.Conn) {
	// Each line is written with hardcoded visible-width spacing so that
	// ANSI escape bytes don't disturb column alignment in terminals.
	w := func(s string) { conn.Write([]byte(s + "\n")) }
	w(colorOK() + "\n[AVAILABLE COMMANDS]")
	w(sep())
	w(colorBold("FLEET OPS"))
	w("  " + colorHeader("PING") + "                                   Check connectivity")
	w("  " + colorHeader("REPORT") + "                                 System telemetry")
	w(colorBold("FILE OPS"))
	w("  " + colorHeader("TOUCH") + " <PATH>                           Create empty file")
	w("  " + colorHeader("LIST") + " [PATH]                            List directory")
	w("  " + colorHeader("TREE") + " [PATH] [DEPTH]                    Recursive listing")
	w("  " + colorHeader("CAT") + " <PATH>                             Read file + CRC32")
	w("  " + colorHeader("WRITE") + " <PATH> <CONTENT>                 Write single line")
	w("  " + colorHeader("WRITEML") + " <PATH> <DELIM>                 Write multi-line")
	w("  " + colorHeader("APPEND") + " <PATH> <CONTENT>                Append to file")
	w(colorBold("COMMS"))
	w("  " + colorHeader("NICK") + " <CALLSIGN>                        Set display name")
	w("  " + colorHeader("WHO") + "                                    List commanders")
	w("  " + colorHeader("WHISPER") + " <TARGET> <MSG>                 Private message")
	w("  " + colorHeader("BROADCAST") + " <MSG>                        Message all nodes")
	w(colorBold("OTHER"))
	w("  " + colorHeader("CHALLENGE") + "                              Security puzzle")
	w("  " + colorHeader("HELP") + "                                   This listing")
	w("  " + colorHeader("EXIT") + "                                   Disconnect")
	w(sep())
}

func getSystemReport() string {
	up := time.Since(serverStart)
	days := int(up.Hours()) / 24
	hours := int(up.Hours()) % 24
	mins := int(up.Minutes()) % 60
	secs := int(up.Seconds()) % 60
	uptimeStr := fmt.Sprintf("%dd %02dh %02dm %02ds", days, hours, mins, secs)

	return colorOK() + "\n" +
		colorHeader("--------------------------------") + "\n" +
		"UNIT STATUS : OPERATIONAL\n" +
		fmt.Sprintf("OS          : %s\n", runtime.GOOS) +
		fmt.Sprintf("ARCH        : %s\n", runtime.GOARCH) +
		fmt.Sprintf("TIME        : %s\n", time.Now().Format(time.RFC1123)) +
		fmt.Sprintf("UPTIME      : %s\n", uptimeStr) +
		colorHeader("--------------------------------") + "\n"
}

func handleTouch(conn net.Conn, path string) {
	path, err := sanitizePath(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	if err := ensureParentDir(path); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	defer file.Close()
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[CREATING TARGET: %s]\n[FILE SYSTEM ACKNOWLEDGED]\n", path)))
}

func handleList(conn net.Conn, path string) {
	entries, err := getDirectoryListing(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[SCANNING SECTOR: <%s>]\n", path)))
	conn.Write([]byte(sep() + "\n"))
	conn.Write([]byte("TYPE    SIZE        NAME\n"))
	conn.Write([]byte(sep() + "\n"))
	for _, e := range entries {
		entryType := "FILE"
		if e.IsDir {
			// Color DIR cyan to match TREE visual language; FILE stays plain.
			entryType = colorHeader("DIR")
		}
		conn.Write([]byte(fmt.Sprintf("%-7s  %-10d  %s\n", entryType, e.Size, e.Name)))
	}
	conn.Write([]byte(sep() + "\n"))
	conn.Write([]byte(fmt.Sprintf("[SCAN COMPLETE: %d OBJECTS]\n", len(entries))))
}

func handleCat(conn net.Conn, path string) {
	path, err := sanitizePath(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: FILE NOT FOUND: %s]", path)) + "\nFAIL\n"))
		return
	}
	if info.IsDir() {
		conn.Write([]byte(colorErr("[ERROR: TARGET IS A DIRECTORY]") + "\nFAIL\n"))
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	checksum := crc32.ChecksumIEEE(content)
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[OPENING FILE: <%s>]\n[SIZE: %d BYTES]\n", path, len(content))))
	conn.Write([]byte(colorHeader("[BEGIN DATA STREAM]") + "\n"))
	conn.Write(content)
	conn.Write([]byte(colorHeader("\n[END DATA STREAM]") + "\n"))
	conn.Write([]byte(fmt.Sprintf("[TRANSFER COMPLETE: %d BYTES | CRC32: %08X]\n", len(content), checksum)))
}

func handleWrite(conn net.Conn, path string, content string) {
	path, err := sanitizePath(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	if err := ensureParentDir(path); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[TARGET ACQUIRED: %s]\n[WRITE COMPLETE]\n", path)))
}

func handleWriteML(conn net.Conn, scanner *bufio.Scanner, path string, delimiter string) {
	safePath, err := sanitizePath(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	if err := ensureParentDir(safePath); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[INITIATING WRITE STREAM TO: %s]\n[TERMINATOR SET TO: '%s']\n[BEGIN TRANSMISSION...]\n", safePath, delimiter)))

	var lines []string
	delimiterFound := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == delimiter {
			delimiterFound = true
			break
		}
		lines = append(lines, line)
	}

	if !delimiterFound {
		fmt.Printf("[WARN] STREAM INTERRUPTED FROM CLIENT, WRITE ABORTED TO %s\n", safePath)
		return
	}

	content := []byte(strings.Join(lines, "\n"))
	if err := os.WriteFile(safePath, content, 0644); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: WRITE FAILED: %v]", err)) + "\nFAIL\n"))
		return
	}
	checksum := crc32.ChecksumIEEE(content)
	conn.Write([]byte(fmt.Sprintf("[EOF RECEIVED]\n[CAPTURED %d LINES]\n[BYTES WRITTEN: %d | CRC32: %08X]\n", len(lines), len(content), checksum)))
}

func handleAppend(conn net.Conn, path string, content string) {
	path, err := sanitizePath(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	if err := ensureParentDir(path); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	prefix := ""
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 && existing[len(existing)-1] != '\n' {
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	defer f.Close()
	if _, err := f.WriteString(prefix + content); err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[TARGET ACQUIRED: %s]\n[APPEND COMPLETE]\n", path)))
}

func handleTree(conn net.Conn, path string, maxDepth int) {
	path, err := sanitizePath(path)
	if err != nil {
		conn.Write([]byte(colorErr(fmt.Sprintf("[ERROR: %v]", err)) + "\nFAIL\n"))
		return
	}
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[SCANNING SECTOR: %s]\n[MAX DEPTH: %d]\n", path, maxDepth)))
	conn.Write([]byte(sep() + "\n"))
	conn.Write([]byte(path + "\n"))
	count := 0
	buildTree(conn, path, "", maxDepth, 0, &count)
	conn.Write([]byte(sep() + "\n"))
	conn.Write([]byte(fmt.Sprintf("[SCAN COMPLETE: %d OBJECTS]\n", count)))
}

func buildTree(conn net.Conn, dir string, prefix string, maxDepth int, depth int, count *int) {
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for i, entry := range entries {
		*count++
		isLast := i == len(entries)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		name := entry.Name()
		if entry.IsDir() {
			// Directories: bold cyan with trailing "/" to match LIST visual language.
			name = colorDirName(name + "/")
		}
		conn.Write([]byte(prefix + connector + name + "\n"))
		if entry.IsDir() {
			buildTree(conn, filepath.Join(dir, entry.Name()), childPrefix, maxDepth, depth+1, count)
		}
	}
}

func handleNick(conn net.Conn, callsign string) {
	if !nickPattern.MatchString(callsign) {
		conn.Write([]byte(colorErr("[ERROR: INVALID CALLSIGN (1-16 ALPHANUMERIC/UNDERSCORE)]") + "\nFAIL\n"))
		return
	}
	clients.mu.Lock()
	defer clients.mu.Unlock()
	for c, info := range clients.m {
		if c != conn && strings.EqualFold(info.Nick, callsign) {
			conn.Write([]byte(colorErr("[ERROR: CALLSIGN ALREADY IN USE]") + "\nFAIL\n"))
			return
		}
	}
	clients.m[conn].Nick = callsign
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[CALLSIGN REGISTERED: %s]\n", callsign)))
}

func handleWho(conn net.Conn) {
	clients.mu.RLock()
	defer clients.mu.RUnlock()
	conn.Write([]byte(fmt.Sprintf(colorOK()+"\n[CONNECTED COMMANDERS: %d]\n", len(clients.m))))
	conn.Write([]byte(sep() + "\n"))
	conn.Write([]byte(fmt.Sprintf("%-3s  %-18s  %-21s  %s\n", "#", "CALLSIGN", "ADDRESS", "UPTIME")))
	conn.Write([]byte(sep() + "\n"))
	i := 1
	for _, info := range clients.m {
		nick := info.Nick
		if nick == "" {
			nick = "-"
		}
		conn.Write([]byte(fmt.Sprintf("%-3d  %-18s  %-21s  %s\n", i, nick, info.Addr, formatUptime(time.Since(info.ConnectedAt)))))
		i++
	}
	conn.Write([]byte(sep() + "\n"))
}

// --- HELPERS ---

func sendMOTD(conn net.Conn) {
	content, err := os.ReadFile(".motd")
	if err != nil || len(content) == 0 {
		return
	}
	truncated := false
	if len(content) > 512 {
		content = content[:512]
		truncated = true
	}
	conn.Write([]byte(colorHeader("[MESSAGE OF THE DAY]\n========================================") + "\n"))
	conn.Write(content)
	if truncated {
		conn.Write([]byte("\n[...TRUNCATED]"))
	}
	conn.Write([]byte(colorHeader("\n========================================") + "\n"))
}

func formatUptime(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func getDirectoryListing(path string) ([]FileEntry, error) {
	path, err := sanitizePath(path)
	if err != nil {
		return nil, err
	}
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	result := make([]FileEntry, 0, len(dirEntries))
	for _, e := range dirEntries {
		fullPath := filepath.Join(path, e.Name())
		var size int64
		if !e.IsDir() {
			size, _ = DirSize(fullPath)
		} else {
			if info, err := e.Info(); err == nil {
				size = info.Size()
			}
		}
		result = append(result, FileEntry{Name: e.Name(), IsDir: e.IsDir(), Size: size})
	}
	return result, nil
}

func DirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func sanitizePath(path string) (string, error) {
	if strings.HasPrefix(path, "/") {
		path = "." + path
	} else if !strings.HasPrefix(path, ".") {
		path = "./" + path
	}
	cleaned := filepath.Clean(path)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}
	cwd, _ := os.Getwd()
	if !strings.HasPrefix(abs, cwd) {
		return "", fmt.Errorf("access denied: path escapes working directory")
	}
	return cleaned, nil
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}
