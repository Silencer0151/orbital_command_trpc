package main

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"bufio"
)

// Challenge represents a single auth challenge with everything the client needs to solve it
type Challenge struct {
	Prompt string
	Answer string
}

// --- CHALLENGE GENERATORS ---

// Binary operation challenge (XOR, AND, OR) on 4-bit strings
func genBinaryOpChallenge() Challenge {
	ops := []struct {
		Name string
		Fn   func(a, b int) int
		Ref  string
	}{
		{"XOR", func(a, b int) int { return a ^ b }, "0^0=0  0^1=1  1^0=1  1^1=0"},
		{"AND", func(a, b int) int { return a & b }, "0&0=0  0&1=0  1&0=0  1&1=1"},
		{"OR", func(a, b int) int { return a | b },  "0|0=0  0|1=1  1|0=1  1|1=1"},
	}

	op := ops[rand.Intn(len(ops))]
	a := rand.Intn(16) // 0-15 fits in 4 bits
	b := rand.Intn(16)
	result := op.Fn(a, b)

	prompt := fmt.Sprintf(""+
		"[COMPUTE HASH]\n"+
		"OPERAND A  : %04b\n"+
		"OPERAND B  : %04b\n"+
		"OPERATION  : %s\n"+
		"----------------------------------\n"+
		"TRUTH TABLE:\n"+
		"  %s: %s\n"+
		"----------------------------------\n",
		a, b, op.Name, op.Name, op.Ref)

	answer := fmt.Sprintf("%04b", result)
	return Challenge{Prompt: prompt, Answer: answer}
}

// Arithmetic challenge dressed up as signal calibration
func genArithmeticChallenge() Challenge {
	type arithOp struct {
		Name   string
		Symbol string
		Fn     func(a, b int) int
	}

	ops := []arithOp{
		{"MULTIPLY", "*", func(a, b int) int { return a * b }},
		{"ADD", "+", func(a, b int) int { return a + b }},
		{"SUBTRACT", "-", func(a, b int) int { return a - b }},
	}

	op := ops[rand.Intn(len(ops))]

	var a, b int
	switch op.Name {
	case "MULTIPLY":
		a = rand.Intn(12) + 2 // 2-13
		b = rand.Intn(12) + 2
	case "SUBTRACT":
		a = rand.Intn(50) + 20 // ensure positive result
		b = rand.Intn(20) + 1
	default: // ADD
		a = rand.Intn(90) + 10
		b = rand.Intn(90) + 10
	}

	result := op.Fn(a, b)

	prompt := fmt.Sprintf(""+
		"[CALIBRATE FREQUENCY]\n"+
		"SIGNAL A   : %d\n"+
		"SIGNAL B   : %d\n"+
		"MODULATOR  : %s (%s)\n"+
		"----------------------------------\n"+
		"COMPUTE: %d %s %d = ?\n"+
		"----------------------------------\n",
		a, b, op.Name, op.Symbol, a, op.Symbol, b)

	answer := fmt.Sprintf("%d", result)
	return Challenge{Prompt: prompt, Answer: answer}
}

// ASCII sum challenge with the lookup table provided
func genASCIIChallenge() Challenge {
	words := []string{"ACE", "BYTE", "CMD", "DATA", "EXEC", "FLAG", "GRID", "HEX", "ION", "KEY", "LOG", "MEM", "NET", "ORB", "PORT", "RUN", "SYS", "TCP", "UP", "VEC"}

	word := words[rand.Intn(len(words))]

	var sum int
	var tableLines []string
	for _, ch := range word {
		val := int(ch)
		sum += val
		tableLines = append(tableLines, fmt.Sprintf("  '%c' = %d", ch, val))
	}

	prompt := fmt.Sprintf(""+
		"[DECODE TRANSMISSION]\n"+
		"PAYLOAD    : \"%s\"\n"+
		"OPERATION  : SUM ASCII VALUES\n"+
		"----------------------------------\n"+
		"CIPHER TABLE:\n"+
		"%s\n"+
		"----------------------------------\n",
		word, strings.Join(tableLines, "\n"))

	answer := fmt.Sprintf("%d", sum)
	return Challenge{Prompt: prompt, Answer: answer}
}

// GenerateChallenge picks a random challenge type
func GenerateChallenge() Challenge {
	generators := []func() Challenge{
		genBinaryOpChallenge,
		genArithmeticChallenge,
		genASCIIChallenge,
	}
	return generators[rand.Intn(len(generators))]()
}

// RunAuthChallenge presents a challenge and loops until the client solves it.
// Returns true if authenticated, false if the connection was lost.
func RunAuthChallenge(conn net.Conn, scanner *bufio.Scanner) bool {
	challenge := GenerateChallenge()

	conn.Write([]byte("\n"))
	conn.Write([]byte("==================================\n"))
	conn.Write([]byte("  SECURITY CLEARANCE REQUIRED     \n"))
	conn.Write([]byte("==================================\n"))
	conn.Write([]byte(challenge.Prompt))
	conn.Write([]byte("[ENTER RESULT] > "))

	for scanner.Scan() {
		response := strings.TrimSpace(scanner.Text())

		if response == "" {
			conn.Write([]byte("[ENTER RESULT] > "))
			continue
		}

		if response == challenge.Answer {
			conn.Write([]byte("\n[HASH VERIFIED]\n"))
			conn.Write([]byte("[CLEARANCE GRANTED]\n"))
			conn.Write([]byte("[WELCOME, COMMANDER]\n"))
			return true
		}

		// Wrong answer - generate a new challenge
		conn.Write([]byte(fmt.Sprintf("\n[HASH MISMATCH: EXPECTED %s, GOT %s]\n", challenge.Answer, response)))
		conn.Write([]byte("[RECALIBRATING...]\n\n"))

		challenge = GenerateChallenge()
		conn.Write([]byte(challenge.Prompt))
		conn.Write([]byte("[ENTER RESULT] > "))
	}

	return false // connection lost
}
