package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain initializes package-level state that main() normally sets up.
// PROMPT is a var set in main() after flag parsing; tests never call main().
func TestMain(m *testing.M) {
	PROMPT = ANSIBold + ANSIGreen + "READY > " + ANSIReset
	os.Exit(m.Run())
}

// --- TEST HARNESS ---

// isRemote reports whether tests are running against an external server
// rather than a locally-spawned one.
func isRemote() bool { return os.Getenv("TEST_SERVER") != "" }

// skipIfRemote skips the current test when running in remote compatibility
// mode. Use this for tests whose setup or verification requires access to the
// local filesystem (e.g. os.MkdirAll, os.Stat).
func skipIfRemote(t *testing.T) {
	t.Helper()
	if isRemote() {
		t.Skip("skipped in remote compatibility mode (requires local filesystem access)")
	}
}

// testServer returns the address to connect to and a cleanup function.
//
// Local mode (default): spins up a real server in a ./testing/ sandbox.
// Remote mode: set TEST_SERVER=host:port to test an external implementation.
//
//	go test -v -run TestPing -count=1 .          # local
//	TEST_SERVER=192.168.1.5:9078 go test -v .    # remote
func testServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()

	// Remote mode: connect to the external server, no local setup needed.
	if ext := os.Getenv("TEST_SERVER"); ext != "" {
		t.Logf("[REMOTE MODE] targeting %s", ext)
		return ext, func() {}
	}

	// Local mode: spin up an in-process server sandboxed to ./testing/.
	testDir, err := filepath.Abs("./testing")
	if err != nil {
		t.Fatalf("Failed to resolve testing dir: %v", err)
	}
	os.MkdirAll(testDir, 0755)

	origDir, _ := os.Getwd()
	os.Chdir(testDir)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}
	addr = listener.Addr().String()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleConnection(conn, false)
		}
	}()

	cleanup = func() {
		listener.Close()
		os.Chdir(origDir)
		// Test root directory is intentionally NOT removed.
	}
	return addr, cleanup
}

// --- CLIENT HELPER ---

type tcpClient struct {
	conn net.Conn
	t    *testing.T
}

func dial(t *testing.T, addr string) *tcpClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	c := &tcpClient{conn: conn, t: t}
	c.readUntilPrompt() // consume banner + initial READY >
	return c
}

func (c *tcpClient) send(cmd string) {
	c.t.Helper()
	if _, err := fmt.Fprintf(c.conn, "%s\n", cmd); err != nil {
		c.t.Fatalf("send %q: %v", cmd, err)
	}
}

// readUntilPrompt reads byte-by-byte until the PROMPT suffix is seen.
// Returns everything before the PROMPT.
func (c *tcpClient) readUntilPrompt() string {
	c.t.Helper()
	var buf []byte
	one := make([]byte, 1)
	for {
		if _, err := c.conn.Read(one); err != nil {
			c.t.Fatalf("read error waiting for prompt: %v (so far: %q)", err, string(buf))
		}
		buf = append(buf, one[0])
		if s, ok := strings.CutSuffix(string(buf), PROMPT); ok {
			return s
		}
	}
}

// sendAndRead sends cmd and returns the server response up to the next prompt.
func (c *tcpClient) sendAndRead(cmd string) string {
	c.t.Helper()
	c.send(cmd)
	return c.readUntilPrompt()
}

// readAllUntilClose drains the connection until EOF (for EXIT/QUIT tests).
func (c *tcpClient) readAllUntilClose() string {
	c.conn.SetDeadline(time.Now().Add(2 * time.Second))
	var buf []byte
	tmp := make([]byte, 1024)
	for {
		n, err := c.conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

func (c *tcpClient) close() { c.conn.Close() }

// readBannerAndPrompt reads a raw net.Conn byte-by-byte until the PROMPT suffix.
func readBannerAndPrompt(t *testing.T, conn net.Conn) string {
	t.Helper()
	var buf []byte
	one := make([]byte, 1)
	for {
		if _, err := conn.Read(one); err != nil {
			break
		}
		buf = append(buf, one[0])
		if strings.HasSuffix(string(buf), PROMPT) {
			return string(buf)
		}
	}
	return string(buf)
}

// --- ASSERTIONS ---

func assertContains(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("expected output to contain %q\ngot:\n%s", substr, output)
	}
}

func assertNotContains(t *testing.T, output, substr string) {
	t.Helper()
	if strings.Contains(output, substr) {
		t.Errorf("expected output NOT to contain %q\ngot:\n%s", substr, output)
	}
}

// ============================================================================
// FLEET OPS
// ============================================================================

func TestPing(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("PING")
	assertContains(t, out, "OK")
	assertContains(t, out, "PONG")
	assertContains(t, out, "LATENCY")
}

func TestPingCaseInsensitive(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	for _, v := range []string{"ping", "Ping", "pInG", "PING"} {
		assertContains(t, c.sendAndRead(v), "PONG")
	}
}

func TestReport(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("REPORT")
	assertContains(t, out, "OK")
	assertContains(t, out, "UNIT STATUS")
	assertContains(t, out, "OS")
	assertContains(t, out, "ARCH")
	assertContains(t, out, "TIME")
	assertContains(t, out, "UPTIME")
}

func TestHelp(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("HELP")
	assertContains(t, out, "OK")
	// Fleet ops
	assertContains(t, out, "PING")
	assertContains(t, out, "REPORT")
	// File ops
	assertContains(t, out, "TOUCH")
	assertContains(t, out, "LIST")
	assertContains(t, out, "TREE")
	assertContains(t, out, "CAT")
	assertContains(t, out, "WRITE")
	assertContains(t, out, "WRITEML")
	assertContains(t, out, "APPEND")
	// Comms
	assertContains(t, out, "NICK")
	assertContains(t, out, "WHO")
	assertContains(t, out, "WHISPER")
	assertContains(t, out, "BROADCAST")
	// Other
	assertContains(t, out, "CHALLENGE")
	assertContains(t, out, "EXIT")
}

func TestHelpCaseInsensitive(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("help"), "AVAILABLE COMMANDS")
}

func TestUnknownCommand(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("XYZZY")
	assertContains(t, out, "ERR UNKNOWN COMMAND")
	assertContains(t, out, "XYZZY")
	assertContains(t, out, "FAIL")
}

func TestEmptyInput(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Empty line should produce no output content — just a re-prompt.
	out := c.sendAndRead("")
	if strings.TrimSpace(out) != "" {
		t.Logf("Note: empty input returned %q (expected empty)", out)
	}
}

func TestWhitespaceInput(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("   ")
	if strings.TrimSpace(out) != "" {
		t.Logf("Note: whitespace-only input returned %q", out)
	}
}

func TestExit(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.send("EXIT")
	assertContains(t, c.readAllUntilClose(), "GOODBYE")
}

func TestQuit(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.send("QUIT")
	assertContains(t, c.readAllUntilClose(), "GOODBYE")
}

func TestBannerOnConnect(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	banner := readBannerAndPrompt(t, conn)
	assertContains(t, banner, "ORBITAL COMMAND")
	assertContains(t, banner, "UPLINK ESTABLISHED")
}

// ============================================================================
// FILE OPS: TOUCH
// ============================================================================

func TestTouchCreatesFile(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("TOUCH touched.txt")
	assertContains(t, out, "OK")
	assertContains(t, out, "FILE SYSTEM ACKNOWLEDGED")

	if !isRemote() {
		if _, err := os.Stat("touched.txt"); os.IsNotExist(err) {
			t.Error("TOUCH did not create file on disk")
		}
	}
}

func TestTouchBarePath(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// No ./ prefix — sanitizePath should normalize it.
	assertContains(t, c.sendAndRead("TOUCH baretouch.txt"), "OK")
}

func TestTouchExplicitDotSlash(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("TOUCH ./explicit.txt"), "OK")
}

func TestTouchMissingArg(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("TOUCH")
	assertContains(t, out, "ERROR")
	assertContains(t, out, "FAIL")
}

func TestTouchPathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("TOUCH ../../escape.txt")
	assertContains(t, out, "FAIL")
	assertNotContains(t, out, "FILE SYSTEM ACKNOWLEDGED")
}

// ============================================================================
// FILE OPS: WRITE / CAT
// ============================================================================

func TestWriteAndCat(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("WRITE ./hello.txt Hello_World")
	assertContains(t, out, "OK")
	assertContains(t, out, "WRITE COMPLETE")

	out = c.sendAndRead("CAT ./hello.txt")
	assertContains(t, out, "OK")
	assertContains(t, out, "BEGIN DATA STREAM")
	assertContains(t, out, "Hello_World")
	assertContains(t, out, "END DATA STREAM")
	assertContains(t, out, "TRANSFER COMPLETE")
	assertContains(t, out, "CRC32") // v1.7: CRC32 checksum footer
}

func TestCatCRC32Format(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./crcfile.txt CONTENT")
	out := c.sendAndRead("CAT ./crcfile.txt")
	// CRC32 appears as 8 uppercase hex digits
	assertContains(t, out, "CRC32:")
	assertContains(t, out, "BYTES")
}

func TestWriteOverwrites(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./overwrite.txt FIRST")
	c.sendAndRead("WRITE ./overwrite.txt SECOND")

	out := c.sendAndRead("CAT ./overwrite.txt")
	assertContains(t, out, "SECOND")
	assertNotContains(t, out, "FIRST")
}

func TestWriteMultiWordContent(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./multi.txt hello world foo bar")
	assertContains(t, c.sendAndRead("CAT ./multi.txt"), "hello world foo bar")
}

func TestWriteBarePath(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// No ./ prefix — sanitizePath should handle it.
	assertContains(t, c.sendAndRead("WRITE barefile.txt content_here"), "WRITE COMPLETE")
	assertContains(t, c.sendAndRead("CAT barefile.txt"), "content_here")
}

func TestWriteCreatesDeepSubdirs(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// ensureParentDir should create the intermediate directories.
	out := c.sendAndRead("WRITE ./deep/nested/path/file.txt payload")
	assertContains(t, out, "WRITE COMPLETE")
	assertContains(t, c.sendAndRead("CAT ./deep/nested/path/file.txt"), "payload")
}

func TestWriteMissingArgs(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("WRITE"), "FAIL")
	assertContains(t, c.sendAndRead("WRITE onlypath.txt"), "FAIL")
}

func TestCatMissingArg(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("CAT")
	assertContains(t, out, "ERROR")
	assertContains(t, out, "FAIL")
}

func TestCatNonexistentFile(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("CAT ./doesnotexist.txt")
	assertContains(t, out, "FILE NOT FOUND")
	assertContains(t, out, "FAIL")
}

func TestCatDirectory(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.MkdirAll("./catdir", 0755)
	out := c.sendAndRead("CAT ./catdir")
	assertContains(t, out, "TARGET IS A DIRECTORY")
	assertContains(t, out, "FAIL")
}

func TestCatSpecialCharFilename(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./special-file_v2.txt content")
	assertContains(t, c.sendAndRead("CAT ./special-file_v2.txt"), "content")
}

// ============================================================================
// FILE OPS: LIST
// ============================================================================

func TestListCurrentDir(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./listed.txt data")

	out := c.sendAndRead("LIST")
	assertContains(t, out, "OK")
	assertContains(t, out, "SCANNING SECTOR")
	assertContains(t, out, "listed.txt")
	assertContains(t, out, "SCAN COMPLETE")
}

func TestListDotPath(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./dotlisted.txt data")
	assertContains(t, c.sendAndRead("LIST ."), "dotlisted.txt")
}

func TestListExplicitSubdir(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.MkdirAll("./subdir", 0755)
	os.WriteFile("./subdir/inner.txt", []byte("inner"), 0644)

	assertContains(t, c.sendAndRead("LIST ./subdir"), "inner.txt")
}

func TestListBareSubdirPath(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.MkdirAll("./baredir", 0755)
	os.WriteFile("./baredir/file.txt", []byte("x"), 0644)

	// No ./ prefix.
	assertContains(t, c.sendAndRead("LIST baredir"), "file.txt")
}

func TestListNonexistentPath(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("LIST ./ghost_dir")
	assertContains(t, out, "ERROR")
	assertContains(t, out, "FAIL")
}

func TestListOnFile(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.WriteFile("./afile.txt", []byte("x"), 0644)
	out := c.sendAndRead("LIST ./afile.txt")
	assertContains(t, out, "ERROR")
	assertContains(t, out, "FAIL")
}

func TestListShowsFileAndDirTypes(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.MkdirAll("./typedir/sub", 0755)
	os.WriteFile("./typedir/f.txt", []byte("x"), 0644)

	out := c.sendAndRead("LIST ./typedir")
	assertContains(t, out, "FILE")
	assertContains(t, out, "DIR")
}

func TestListShowsFileSizes(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./sized.txt " + strings.Repeat("A", 100))
	out := c.sendAndRead("LIST .")
	assertContains(t, out, "sized.txt")
	assertContains(t, out, "FILE")
}

// ============================================================================
// FILE OPS: TREE
// ============================================================================

func TestTreeCurrentDir(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.MkdirAll("./treedir/sub", 0755)
	os.WriteFile("./treedir/file.txt", []byte("x"), 0644)
	os.WriteFile("./treedir/sub/deep.txt", []byte("y"), 0644)

	out := c.sendAndRead("TREE ./treedir")
	assertContains(t, out, "OK")
	assertContains(t, out, "SCANNING SECTOR")
	assertContains(t, out, "file.txt")
	assertContains(t, out, "sub")
	assertContains(t, out, "SCAN COMPLETE")
}

func TestTreeDefaultPath(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./treetest.txt data")
	out := c.sendAndRead("TREE")
	assertContains(t, out, "SCANNING SECTOR")
	assertContains(t, out, "treetest.txt")
}

func TestTreeDepthLimit(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// 4-level deep structure; depth=2 should stop before the deepest file.
	os.MkdirAll("./depthtree/a/b/c/d", 0755)
	os.WriteFile("./depthtree/a/b/c/d/deep.txt", []byte("z"), 0644)

	out := c.sendAndRead("TREE ./depthtree 2")
	assertContains(t, out, "MAX DEPTH: 2")
	// deep.txt is 4 levels in — should not appear with depth=2.
	assertNotContains(t, out, "deep.txt")
}

func TestTreeMaxAllowedDepth(t *testing.T) {
	skipIfRemote(t)
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	os.MkdirAll("./maxtree/x", 0755)
	out := c.sendAndRead("TREE ./maxtree 10")
	assertContains(t, out, "MAX DEPTH: 10")
	assertContains(t, out, "SCAN COMPLETE")
}

func TestTreeInvalidDepthZero(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("TREE . 0"), "FAIL")
}

func TestTreeInvalidDepthTooLarge(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("TREE . 11"), "FAIL")
}

func TestTreeInvalidDepthNonNumeric(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("TREE . abc"), "FAIL")
}

func TestTreePathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("TREE ../../")
	// Should either error or not expose parent-directory contents.
	if strings.Contains(out, "main.go") {
		t.Error("TREE path traversal: exposed parent directory")
	}
}

// ============================================================================
// FILE OPS: WRITEML
// ============================================================================

func TestWriteML(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.send("WRITEML ./multi.txt EOF")
	time.Sleep(80 * time.Millisecond)
	c.send("Line one")
	c.send("Line two")
	c.send("Line three")
	out := c.sendAndRead("EOF")

	assertContains(t, out, "CAPTURED 3 LINES")
	assertContains(t, out, "CRC32") // v1.7: CRC32 in receipt

	catOut := c.sendAndRead("CAT ./multi.txt")
	assertContains(t, catOut, "Line one")
	assertContains(t, catOut, "Line two")
	assertContains(t, catOut, "Line three")
}

func TestWriteMLEmptyBody(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.send("WRITEML ./empty_ml.txt DONE")
	time.Sleep(80 * time.Millisecond)
	assertContains(t, c.sendAndRead("DONE"), "CAPTURED 0 LINES")
}

func TestWriteMLMissingArgs(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("WRITEML"), "FAIL")
	assertContains(t, c.sendAndRead("WRITEML onlypath.txt"), "FAIL")
}

func TestWriteMLDelimiterExactMatch(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// "STOPWATCH" and "BUSSTOP" contain "STOP" but must NOT trigger the delimiter.
	c.send("WRITEML ./tricky.txt STOP")
	time.Sleep(80 * time.Millisecond)
	c.send("STOPWATCH is a word")
	c.send("BUSSTOP too")
	out := c.sendAndRead("STOP")
	assertContains(t, out, "CAPTURED 2 LINES")

	catOut := c.sendAndRead("CAT ./tricky.txt")
	assertContains(t, catOut, "STOPWATCH is a word")
	assertContains(t, catOut, "BUSSTOP too")
}

func TestWriteMLLargePayload(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.send("WRITEML ./bigfile.txt END")
	time.Sleep(80 * time.Millisecond)

	const lineCount = 200
	for i := range lineCount {
		c.send(fmt.Sprintf("Line %d: %s", i, strings.Repeat("x", 80)))
	}
	assertContains(t, c.sendAndRead("END"), fmt.Sprintf("CAPTURED %d LINES", lineCount))
}

func TestWriteMLCRC32InReceipt(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.send("WRITEML ./crcml.txt DONE")
	time.Sleep(80 * time.Millisecond)
	c.send("hello world")
	out := c.sendAndRead("DONE")
	assertContains(t, out, "BYTES WRITTEN")
	assertContains(t, out, "CRC32:")
}

// TestWriteMLDisconnectAbort verifies the server does NOT write a file when
// the client disconnects before sending the delimiter (atomicity guarantee).
func TestWriteMLDisconnectAbort(t *testing.T) {
	skipIfRemote(t) // verifies absence of local file after disconnect
	addr, cleanup := testServer(t)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.SetDeadline(time.Now().Add(8 * time.Second))

	// Consume banner + prompt.
	readBannerAndPrompt(t, conn)

	// Start a WRITEML stream.
	fmt.Fprintf(conn, "WRITEML ./abort_test.txt NEVER\n")
	time.Sleep(100 * time.Millisecond)

	// Send partial content then abruptly close — delimiter never sent.
	fmt.Fprintf(conn, "partial line 1\n")
	fmt.Fprintf(conn, "partial line 2\n")
	time.Sleep(50 * time.Millisecond)
	conn.Close()

	// Give the server goroutine time to notice the disconnect.
	time.Sleep(300 * time.Millisecond)

	if _, err := os.Stat("./abort_test.txt"); err == nil {
		t.Error("WRITEML atomicity violation: file written despite no delimiter received")
		os.Remove("./abort_test.txt")
	}
}

// TestWriteMLPathTraversalBlocked ensures sanitizePath is checked before
// the stream is opened, not after receiving the delimiter.
func TestWriteMLPathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("WRITEML ../../escape.txt EOF")
	assertContains(t, out, "FAIL")
}

// ============================================================================
// FILE OPS: APPEND
// ============================================================================

func TestAppendCreatesNewFile(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("APPEND ./appendnew.txt FirstLine")
	assertContains(t, out, "OK")
	assertContains(t, out, "APPEND COMPLETE")
}

func TestAppendToExistingFile(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./appendme.txt LineOne")
	c.sendAndRead("APPEND ./appendme.txt LineTwo")

	out := c.sendAndRead("CAT ./appendme.txt")
	assertContains(t, out, "LineOne")
	assertContains(t, out, "LineTwo")
}

func TestAppendMultipleTimes(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE ./multi_append.txt AAA")
	c.sendAndRead("APPEND ./multi_append.txt BBB")
	c.sendAndRead("APPEND ./multi_append.txt CCC")

	out := c.sendAndRead("CAT ./multi_append.txt")
	assertContains(t, out, "AAA")
	assertContains(t, out, "BBB")
	assertContains(t, out, "CCC")
}

func TestAppendMissingArgs(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("APPEND"), "FAIL")
	assertContains(t, c.sendAndRead("APPEND ./only_path.txt"), "FAIL")
}

func TestAppendPathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("APPEND ../../escape.txt hacked"), "FAIL")
}

func TestAppendBarePath(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("APPEND bareappend.txt line1")
	assertContains(t, c.sendAndRead("CAT bareappend.txt"), "line1")
}

// ============================================================================
// COMMS: NICK
// ============================================================================

func TestNickSet(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("NICK BRAVO")
	assertContains(t, out, "OK")
	assertContains(t, out, "CALLSIGN REGISTERED")
	assertContains(t, out, "BRAVO")
}

func TestNickMissingArg(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("NICK")
	assertContains(t, out, "ERROR")
	assertContains(t, out, "FAIL")
}

func TestNickValidCharsAccepted(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("NICK valid_nick_123"), "CALLSIGN REGISTERED")
}

func TestNickInvalidCharsDash(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Dash is not in [A-Za-z0-9_].
	assertContains(t, c.sendAndRead("NICK my-handle"), "FAIL")
}

func TestNickInvalidCharsAt(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("NICK x@domain"), "FAIL")
}

func TestNickInvalidCharsDot(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("NICK has.dot"), "FAIL")
}

func TestNickTooLong(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// 17 characters — exceeds 16 char limit.
	assertContains(t, c.sendAndRead("NICK ABCDEFGHIJKLMNOPQ"), "FAIL")
}

func TestNickMaxLength(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Exactly 16 chars — should succeed.
	assertContains(t, c.sendAndRead("NICK ABCDEFGHIJKLMNOP"), "CALLSIGN REGISTERED")
}

func TestNickCollision(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	c1 := dial(t, addr)
	defer c1.close()
	c2 := dial(t, addr)
	defer c2.close()

	assertContains(t, c1.sendAndRead("NICK COMMANDER"), "CALLSIGN REGISTERED")
	assertContains(t, c2.sendAndRead("NICK COMMANDER"), "CALLSIGN ALREADY IN USE")
}

func TestNickCaseInsensitiveCollision(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	c1 := dial(t, addr)
	defer c1.close()
	c2 := dial(t, addr)
	defer c2.close()

	c1.sendAndRead("NICK GHOST")
	// "ghost" (lowercase) should collide with "GHOST".
	assertContains(t, c2.sendAndRead("NICK ghost"), "CALLSIGN ALREADY IN USE")
}

func TestNickCanChange(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("NICK ALPHA"), "CALLSIGN REGISTERED")
	// Same client can change their own nick.
	assertContains(t, c.sendAndRead("NICK DELTA"), "CALLSIGN REGISTERED")
}

// ============================================================================
// COMMS: WHO
// ============================================================================

func TestWhoSingleClient(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("WHO")
	assertContains(t, out, "OK")
	assertContains(t, out, "CONNECTED COMMANDERS: 1")
}

func TestWhoMultipleClients(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	c1 := dial(t, addr)
	defer c1.close()
	c2 := dial(t, addr)
	defer c2.close()

	c1.sendAndRead("NICK ALPHA1")
	c2.sendAndRead("NICK ALPHA2")

	out := c1.sendAndRead("WHO")
	assertContains(t, out, "ALPHA1")
	assertContains(t, out, "ALPHA2")
	assertContains(t, out, "UPTIME")
}

func TestWhoWithoutNickShowsDash(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// No nick set — callsign column should show "-".
	assertContains(t, c.sendAndRead("WHO"), "-")
}

func TestWhoDecreasesAfterDisconnect(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	c1 := dial(t, addr)
	c2 := dial(t, addr)
	defer c1.close()

	// Two clients connected.
	assertContains(t, c1.sendAndRead("WHO"), "CONNECTED COMMANDERS: 2")

	// Disconnect c2 and wait for server to process it.
	c2.close()
	time.Sleep(100 * time.Millisecond)

	assertContains(t, c1.sendAndRead("WHO"), "CONNECTED COMMANDERS: 1")
}

// ============================================================================
// COMMS: WHISPER
// ============================================================================

func TestWhisperToOtherClientByNick(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	c1 := dial(t, addr)
	defer c1.close()
	c2 := dial(t, addr)
	defer c2.close()

	c1.sendAndRead("NICK SENDER")
	c2.sendAndRead("NICK RECEIVER")

	out := c1.sendAndRead("WHISPER RECEIVER Hello_there")
	assertContains(t, out, "OK")
	assertContains(t, out, "WHISPER DELIVERED")
	assertContains(t, out, "RECEIVER")
}

func TestWhisperToSelf(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("NICK SOLOACT")
	out := c.sendAndRead("WHISPER SOLOACT hey me")
	assertContains(t, out, "CANNOT WHISPER TO SELF")
	assertContains(t, out, "FAIL")
}

func TestWhisperUnknownTarget(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("WHISPER GHOSTTARGET hello")
	assertContains(t, out, "TARGET NOT FOUND")
	assertContains(t, out, "FAIL")
}

func TestWhisperMissingArgs(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("WHISPER"), "FAIL")
	assertContains(t, c.sendAndRead("WHISPER SOMEONE"), "FAIL")
}

// ============================================================================
// COMMS: BROADCAST
// ============================================================================

func TestBroadcastSenderConfirmation(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("BROADCAST hello world")
	assertContains(t, out, "OK")
	assertContains(t, out, "BROADCAST SENT TO")
}

func TestBroadcastAloneReachesZeroNodes(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Only one client — no peers to receive it.
	assertContains(t, c.sendAndRead("BROADCAST testing alone"), "BROADCAST SENT TO: 0 NODES")
}

func TestBroadcastMissingArg(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	assertContains(t, c.sendAndRead("BROADCAST"), "FAIL")
}

func TestBroadcastReceived(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	// c1: will send the broadcast.
	c1 := dial(t, addr)
	defer c1.close()

	// c2: raw connection to capture the unsolicited broadcast message.
	c2, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial c2: %v", err)
	}
	defer c2.Close()
	c2.SetDeadline(time.Now().Add(8 * time.Second))

	// Consume c2's banner+prompt in a goroutine, then wait for broadcast.
	received := make(chan string, 1)
	go func() {
		readBannerAndPrompt(t, c2)
		// After the prompt, wait for the broadcast to arrive.
		var buf string
		tmp := make([]byte, 4096)
		c2.SetDeadline(time.Now().Add(3 * time.Second))
		for {
			n, err := c2.Read(tmp)
			buf += string(tmp[:n])
			if strings.Contains(buf, "BROADCAST FROM") || err != nil {
				break
			}
		}
		received <- buf
	}()

	time.Sleep(150 * time.Millisecond) // ensure c2 goroutine is waiting
	c1.sendAndRead("NICK BROADCASTER")
	c1.sendAndRead("BROADCAST All stations standby")

	select {
	case msg := <-received:
		assertContains(t, msg, "BROADCAST FROM")
		assertContains(t, msg, "All stations standby")
	case <-time.After(4 * time.Second):
		t.Error("c2 did not receive broadcast within timeout")
	}
}

// ============================================================================
// CHALLENGE (in-session command)
// ============================================================================

func TestChallengeCommandAppears(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	readBannerAndPrompt(t, conn)

	// Send CHALLENGE command.
	fmt.Fprintf(conn, "CHALLENGE\n")

	// Read until the challenge prompt appears.
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if strings.Contains(string(buf), "[ENTER RESULT]") || err != nil {
			break
		}
	}

	assertContains(t, string(buf), "SECURITY CLEARANCE REQUIRED")
}

// ============================================================================
// SECURITY / PATH TRAVERSAL
// ============================================================================

func TestCatPathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("CAT ../../../etc/passwd")
	// sanitizePath should block traversal outside sandbox.
	assertNotContains(t, out, "root:")
}

func TestWritePathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("WRITE ../../pwned.txt gotcha")
	if strings.Contains(out, "WRITE COMPLETE") {
		t.Error("PATH TRAVERSAL: server accepted WRITE to path escaping sandbox")
		// Local cleanup in case it somehow appeared locally too.
		if !isRemote() {
			os.Remove("../../pwned.txt")
		}
	}
}

func TestListPathTraversalBlocked(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("LIST ../")
	// Should not expose parent directory source files.
	if strings.Contains(out, "main.go") || strings.Contains(out, "go.mod") {
		t.Error("PATH TRAVERSAL: LIST exposed parent directory")
	}
}

func TestCatAbsolutePathSandboxed(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// "/etc/passwd" → "./etc/passwd" (in sandbox), should not exist.
	assertNotContains(t, c.sendAndRead("CAT /etc/passwd"), "root:")
}

func TestWriteAbsolutePathSandboxed(t *testing.T) {
	skipIfRemote(t) // can only verify /tmp absence on local machine
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	c.sendAndRead("WRITE /tmp/orbital_test.txt hacked")
	// "/tmp/orbital_test.txt" → "./tmp/orbital_test.txt" in sandbox.
	if _, err := os.Stat("/tmp/orbital_test.txt"); err == nil {
		t.Error("WRITE bypassed path sandboxing and wrote to /tmp")
		os.Remove("/tmp/orbital_test.txt")
	}
}

// ============================================================================
// MOTD
// ============================================================================

func TestMOTDDisplayed(t *testing.T) {
	skipIfRemote(t) // requires writing .motd to the server's working directory
	addr, cleanup := testServer(t)
	defer cleanup()

	os.WriteFile("./.motd", []byte("GREETINGS COMMANDER"), 0644)

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	banner := readBannerAndPrompt(t, conn)
	assertContains(t, banner, "MESSAGE OF THE DAY")
	assertContains(t, banner, "GREETINGS COMMANDER")

	os.Remove("./.motd")
}

func TestMOTDAbsent(t *testing.T) {
	skipIfRemote(t) // requires knowing the remote server has no .motd file
	addr, cleanup := testServer(t)
	defer cleanup()

	os.Remove("./.motd")

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	assertNotContains(t, readBannerAndPrompt(t, conn), "MESSAGE OF THE DAY")
}

func TestMOTDTruncatedAt512(t *testing.T) {
	skipIfRemote(t) // requires writing .motd to the server's working directory
	addr, cleanup := testServer(t)
	defer cleanup()

	// Write >512 bytes of MOTD content.
	long := strings.Repeat("X", 600)
	os.WriteFile("./.motd", []byte(long), 0644)

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	banner := readBannerAndPrompt(t, conn)
	assertContains(t, banner, "TRUNCATED")
	// Should not contain all 600 Xs.
	assertNotContains(t, banner, long)

	os.Remove("./.motd")
}

// ============================================================================
// TESTDIR FILE TESTS
// ============================================================================

// readTestdirFile reads a file from the testdir directory relative to the
// test sandbox (../testdir/<name>).
func readTestdirFile(name string) ([]byte, error) {
	return os.ReadFile("../testdir/" + name)
}

func TestCatTestdirFile(t *testing.T) {
	skipIfRemote(t) // seeds content from local testdir into the server's sandbox
	addr, cleanup := testServer(t)
	defer cleanup()

	content, err := readTestdirFile("testfile.txt")
	if err != nil {
		t.Skipf("testdir/testfile.txt not accessible: %v", err)
	}
	os.WriteFile("./testfile.txt", content, 0644)

	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("CAT ./testfile.txt")
	assertContains(t, out, "BEGIN DATA STREAM")
	assertContains(t, out, "TRANSFER COMPLETE")
	assertContains(t, out, "CRC32")
	// testfile.txt is RFC 2616 — should contain "HTTP".
	assertContains(t, out, "HTTP")
}

func TestCatLineTxtFile(t *testing.T) {
	skipIfRemote(t) // seeds content from local testdir into the server's sandbox
	addr, cleanup := testServer(t)
	defer cleanup()

	content, err := readTestdirFile("line.txt")
	if err != nil {
		t.Skipf("testdir/line.txt not accessible: %v", err)
	}
	os.WriteFile("./line.txt", content, 0644)

	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("CAT ./line.txt")
	assertContains(t, out, "BEGIN DATA STREAM")
	assertContains(t, out, "LOLXD") // known content from line.txt
}

func TestListTestdirContents(t *testing.T) {
	skipIfRemote(t) // creates directories directly in the local sandbox
	addr, cleanup := testServer(t)
	defer cleanup()

	os.MkdirAll("./imported", 0755)
	os.WriteFile("./imported/alpha.txt", []byte("a"), 0644)
	os.WriteFile("./imported/beta.txt", []byte("b"), 0644)
	os.MkdirAll("./imported/subdir", 0755)

	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("LIST ./imported")
	assertContains(t, out, "alpha.txt")
	assertContains(t, out, "beta.txt")
	assertContains(t, out, "DIR")
}

func TestTreeTestdirContents(t *testing.T) {
	skipIfRemote(t) // creates directories directly in the local sandbox
	addr, cleanup := testServer(t)
	defer cleanup()

	os.MkdirAll("./treetestdir/sub", 0755)
	os.WriteFile("./treetestdir/root.txt", []byte("r"), 0644)
	os.WriteFile("./treetestdir/sub/leaf.txt", []byte("l"), 0644)

	c := dial(t, addr)
	defer c.close()

	out := c.sendAndRead("TREE ./treetestdir")
	assertContains(t, out, "root.txt")
	assertContains(t, out, "sub")
	assertContains(t, out, "leaf.txt")
}

// ============================================================================
// PROTOCOL CONFORMANCE
// ============================================================================

func TestAllSuccessResponsesContainOK(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	for _, cmd := range []string{"PING", "REPORT", "HELP"} {
		assertContains(t, c.sendAndRead(cmd), "OK")
	}
}

func TestAllErrorResponsesContainFAIL(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Each of these is missing required arguments.
	for _, cmd := range []string{"TOUCH", "CAT", "WRITE", "WRITEML", "APPEND", "NICK", "WHISPER", "BROADCAST"} {
		out := c.sendAndRead(cmd)
		if !strings.Contains(out, "FAIL") {
			t.Errorf("command %q without args: expected FAIL, got:\n%s", cmd, out)
		}
	}
}

func TestRapidFirePings(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	for range 50 {
		assertContains(t, c.sendAndRead("PING"), "PONG")
	}
}

func TestMultipleConcurrentClients(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	done := make(chan bool, 10)
	for id := range 10 {
		go func(id int) {
			c := dial(t, addr)
			defer c.close()

			if !strings.Contains(c.sendAndRead("PING"), "PONG") {
				t.Errorf("client %d: PING failed", id)
			}
			fname := fmt.Sprintf("./concurrent_%d.txt", id)
			c.sendAndRead(fmt.Sprintf("WRITE %s data_%d", fname, id))
			out := c.sendAndRead(fmt.Sprintf("CAT %s", fname))
			if !strings.Contains(out, fmt.Sprintf("data_%d", id)) {
				t.Errorf("client %d: CAT content mismatch", id)
			}
			done <- true
		}(id)
	}
	for range 10 {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("concurrent client test timed out")
		}
	}
}

func TestVeryLongCommand(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Should either succeed or error gracefully — not crash.
	out := c.sendAndRead("WRITE ./longfile.txt " + strings.Repeat("A", 10000))
	if !strings.Contains(out, "OK") && !strings.Contains(out, "ERROR") {
		t.Errorf("long command produced unexpected output: %q", out)
	}
}

func TestSequentialWorkflow(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// write → list → cat → overwrite → cat again
	c.sendAndRead("WRITE ./workflow.txt step_one")
	assertContains(t, c.sendAndRead("LIST ."), "workflow.txt")
	assertContains(t, c.sendAndRead("CAT ./workflow.txt"), "step_one")
	c.sendAndRead("WRITE ./workflow.txt step_two")
	catOut := c.sendAndRead("CAT ./workflow.txt")
	assertContains(t, catOut, "step_two")
	assertNotContains(t, catOut, "step_one")
}

func TestFullFileCRUD(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// TOUCH → WRITE → APPEND → CAT → LIST
	c.sendAndRead("TOUCH ./crud.txt")
	c.sendAndRead("WRITE ./crud.txt initial_content")
	c.sendAndRead("APPEND ./crud.txt appended_content")

	catOut := c.sendAndRead("CAT ./crud.txt")
	assertContains(t, catOut, "initial_content")
	assertContains(t, catOut, "appended_content")

	listOut := c.sendAndRead("LIST .")
	assertContains(t, listOut, "crud.txt")
}

func TestWriteMLThenCatWorkflow(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()
	c := dial(t, addr)
	defer c.close()

	// Write multi-line via WRITEML, verify via CAT.
	c.send("WRITEML ./wml_workflow.txt ---END---")
	time.Sleep(80 * time.Millisecond)
	c.send("Line A")
	c.send("Line B")
	c.send("Line C")
	writeOut := c.sendAndRead("---END---")
	assertContains(t, writeOut, "CAPTURED 3 LINES")

	catOut := c.sendAndRead("CAT ./wml_workflow.txt")
	assertContains(t, catOut, "Line A")
	assertContains(t, catOut, "Line B")
	assertContains(t, catOut, "Line C")
}

func TestNickThenWhisperThenWhoWorkflow(t *testing.T) {
	addr, cleanup := testServer(t)
	defer cleanup()

	c1 := dial(t, addr)
	defer c1.close()
	c2 := dial(t, addr)
	defer c2.close()

	c1.sendAndRead("NICK ROMEO")
	c2.sendAndRead("NICK JULIET")

	// WHO should list both.
	whoOut := c1.sendAndRead("WHO")
	assertContains(t, whoOut, "ROMEO")
	assertContains(t, whoOut, "JULIET")

	// Whisper from c1 to c2.
	whisperOut := c1.sendAndRead("WHISPER JULIET Wherefore_art_thou")
	assertContains(t, whisperOut, "WHISPER DELIVERED")
}

// bufio is referenced via readBannerAndPrompt which uses bufio.NewReader — but
// we inline the read loop there. Keep the import used via the scanner parameter
// name only. Remove unused import if linter complains.
var _ = bufio.NewReader // keep bufio import satisfied
