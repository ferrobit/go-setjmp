package main

import (
	"fmt"
	"strings"

	"github.com/ferrobit/go-setjmp"
)

type expressionParser struct {
	buf    setjmp.JMPBuf
	input  string
	offset int
	err    error
}

func main() {
	deep := strings.Repeat("(", 10000) + "1"
	sources := []string{"2+3*4", "(1+2)*(3+4)", "-(8-10)*7", "8/(3-3)", "2*(3+", "7 % 2", deep}
	for _, source := range sources {
		label := source
		if length := len(source); length > 20 {
			label = fmt.Sprintf("%v... (%v bytes)", source[:4], length)
		}
		if value, err := evaluate(source); err != nil {
			fmt.Printf("%-24v error: %v\n", label, err)
		} else {
			fmt.Printf("%-24v = %v\n", label, value)
		}
	}
}

func evaluate(source string) (int, error) {
	parser := &expressionParser{input: source}
	defer parser.buf.Free()
	if set, err := parser.buf.Set(); err != nil {
		return 0, fmt.Errorf("failed to set jump buffer: %w", err)
	} else if !set {
		return 0, parser.err
	}
	result := parser.parseExpression()
	if parser.peek() != 0 {
		parser.failUnexpected()
	}
	return result, parser.err
}

func (parser *expressionParser) fail(format string, args ...any) {
	parser.err = fmt.Errorf(format, args...)
	if err := parser.buf.Jump(); err != nil {
		parser.err = fmt.Errorf("failed to report '%v': %w", parser.err, err)
	}
}

func (parser *expressionParser) failUnexpected() {
	if parser.offset >= len(parser.input) {
		parser.fail("unexpected end of input")
	} else {
		parser.fail("unexpected '%c' at offset %v", parser.input[parser.offset], parser.offset)
	}
}

func (parser *expressionParser) parseExpression() int {
	result := parser.parseTerm()
	for {
		switch parser.peek() {
		case '+':
			parser.offset++
			result += parser.parseTerm()
		case '-':
			parser.offset++
			result -= parser.parseTerm()
		default:
			return result
		}
	}
}

func (parser *expressionParser) parseFactor() int {
	switch symbol := parser.peek(); {
	case symbol == '(':
		parser.offset++
		result := parser.parseExpression()
		if parser.peek() != ')' {
			parser.failUnexpected()
		} else {
			parser.offset++
		}
		return result
	case symbol == '-':
		parser.offset++
		return -parser.parseFactor()
	case symbol >= '0' && symbol <= '9':
		return parser.parseNumber()
	default:
		parser.failUnexpected()
		return 0
	}
}

func (parser *expressionParser) parseNumber() int {
	result := 0
	for ; parser.offset < len(parser.input); parser.offset++ {
		if digit := parser.input[parser.offset]; digit < '0' || digit > '9' {
			break
		} else {
			result = result*10 + int(digit-'0')
		}
	}
	return result
}

func (parser *expressionParser) parseTerm() int {
	result := parser.parseFactor()
	for {
		switch parser.peek() {
		case '*':
			parser.offset++
			result *= parser.parseFactor()
		case '/':
			offset := parser.offset
			parser.offset++
			if divisor := parser.parseFactor(); divisor == 0 {
				parser.fail("division by zero at offset %v", offset)
			} else {
				result /= divisor
			}
		default:
			return result
		}
	}
}

func (parser *expressionParser) peek() byte {
	for parser.offset < len(parser.input) && parser.input[parser.offset] == ' ' {
		parser.offset++
	}
	if parser.offset < len(parser.input) {
		return parser.input[parser.offset]
	}
	return 0
}
