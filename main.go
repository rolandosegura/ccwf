package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"text/scanner"
	"text/template"
)

type MessageSymbol struct {
	Name   string
	Fields []Field
}

type Field struct {
	Name    string
	Type    string
	Length  int
	Decimal int // for decimal numbers, is the number of figures to the right of the decimal point
}

type Token rune

func (tok Token) String() string {
	switch tok {
	default:
		return fmt.Sprintf("%c", tok)
	case Message:
		return "message"
	case TypeString:
		return "string"
	case TypeInt, scanner.Int:
		return "int"
	case TypeDecimal, scanner.Float:
		return "decimal"
	case scanner.Ident:
		return "identifier"
	}
	return "???"
}

var (
	lexer      scanner.Scanner
	lookahead  Token
	curmessage MessageSymbol
	curfield   Field
	identname  string // the token value of the id seen
)

const (
	Message = -(iota - scanner.Comment + 1)
	TypeString
	TypeInt
	TypeDecimal
)

func match(tok Token) string {
	if tok != lookahead {
		log.Fatalf("%s:error expecting %v\n", lexer.Pos(), tok)
	}
	toktext := lexer.TokenText()
	lookahead = lexan()
	return toktext
}

func lexan() Token {
	tok := lexer.Scan()
	toktext := lexer.TokenText()
	switch tok {
	default:
		return Token(tok)
	case scanner.Ident:
		switch toktext {
		case "message":
			return Message
		case "int":
			return TypeInt
		case "decimal":
			return TypeDecimal
		case "string":
			return TypeString
		}
		return scanner.Ident
	case scanner.Int:
		return scanner.Int
	}
	return Token(tok)
}

func fieldlist() {
	fielddecl()
	restfielddecl()
}

func fielddecl() {
	curfield.Name = match(scanner.Ident)
	fieldtype()
	curmessage.Fields = append(curmessage.Fields, curfield)
}

func restfielddecl() {
	for {
		if lookahead == Token('}') {
			return
		}
		fielddecl()
	}
}

func fieldtype() {
	switch lookahead {
	case TypeInt, TypeString:
		curfield.Type = match(lookahead)
		match(Token('('))
		l := match(scanner.Int)
		length, _ := strconv.Atoi(l)
		curfield.Length = int(length)
		match(Token(')'))
		match(Token(';'))
	case TypeDecimal:
		curfield.Type = match(lookahead)
		match(Token('('))
		l := match(scanner.Int)
		length, _ := strconv.Atoi(l)
		curfield.Length = int(length)
		match(Token(','))
		l = match(scanner.Int)
		length, _ = strconv.Atoi(l)
		curfield.Decimal = int(length)
		match(Token(')'))
		match(Token(';'))
	default:
		log.Fatalf("%s:error expecting type got %s\n", lexer.Pos(), lookahead)
	}
}

func message() {
	match(Message)
	curmessage.Name = match(scanner.Ident)
	match(Token('{'))
	fieldlist()
	match(Token('}'))
	match(scanner.EOF)
}

func main() {
	flag.Parse()
	fd, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	defer fd.Close()
	lexer.Init(bufio.NewReader(fd))
	lexer.Mode = scanner.ScanComments | scanner.SkipComments | scanner.ScanIdents | scanner.ScanInts
	lexer.Position.Filename = flag.Arg(0)
	lookahead = lexan()
	message()
	tmpl, err := template.New("xsd").Parse(xsdTemplate)
	if err != nil {
		log.Fatalf("parsing template %v\n", err)
	}
	xsdf := flag.Arg(0) + ".xsd"
	fd, err = os.Create(xsdf)
	if err != nil {
		log.Fatal(err)
	}
	defer fd.Close()
	err = tmpl.Execute(fd, curmessage)
	if err != nil {
		log.Fatalf("processing curmessage %v\n", err)
	}
}

var xsdTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema targetNamespace="http://ice.go.cr/{{.Name}}" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
	<xsd:complexType name="{{.Name}}">
		<xsd:sequence>{{range .Fields}}
			<xsd:element minOccurs="1" maxOccurs="1" name="{{.Name}}" type="xsd:{{.Type}}"/>{{end}}
		</xsd:sequence>
	</xsd:complexType>
</xsd:schema>
`
