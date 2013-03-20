package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"text/scanner"
	"text/template"
)

type Operation struct {
	Name string
	In   *Message
	Out  *Message
}

func (op *Operation) emitWSDL(w io.Writer) {
	tmpl, err := template.New("wsdl").Parse(wsdlTemplate)
	if err != nil {
		fatal("parsing wsdl template %v\n", err)
	}
	err = tmpl.Execute(w, op)
	if err != nil {
		fatal("generating wsdl: %v\n", err)
	}
}

func (op *Operation) emitDataHandler(w io.Writer) {
	tmpl, err := template.New("datahandler").Funcs(template.FuncMap{"eq": eq, "lastidx": lastidx}).Parse(datahandlerTemplate)
	if err != nil {
		fatal("parsing datahandler template %v\n", err)
	}
	err = tmpl.Execute(w, op)
	if err != nil {
		fatal("generating datahandler: %v\n", err)
	}
}


// The structure of a CWF message and XSD translation is represented with a Message
type Message struct {
	Name   string
	Fields []*Field
	Length	int	// total length of the CWF string
}

//emitXSD: generates the XSD to w out of the message definition
func (m *Message) emitXSD(w io.Writer) {
	tmpl, err := template.New("xsd").Parse(xsdTemplate)
	if err != nil {
		fatal("parsing template %v\n", err)
	}
	err = tmpl.Execute(w, m)
	if err != nil {
		fatal("generating xsd: %v\n", err)
	}
}

// PrintfFlags return the printf style flags to marshal this message as  a cwf string
func (m *Message) PrintfFlags() string {
	buf := []byte{}
	for _,f := range m.Fields {
		// TODO(rolando) : think about type representation, a more efficient encoding is the token type
		switch f.Type {
		case "int":
			buf = append(buf,[]byte(fmt.Sprintf("%%%dd",f.Length))...)
		case "decimal":
			buf = append(buf,[]byte(fmt.Sprintf("%%%d.%df",f.Length,f.Decimal))...)
		case "string":
			buf = append(buf,[]byte(fmt.Sprintf("%%%ds",f.Length))...)
		default:
			panic(fmt.Errorf("I don't know how to marshal type %v",f.Type))
		}
	}
	
	return fmt.Sprintf("%q",buf)
}


func (m *Message) TestCWFMsg() string {
	buf := []byte{}
	for _, f := range m.Fields {
		fv := make([]byte, f.Length)
		for i := 0; i < len(fv); i++ {
			fv[i] = byte('0') + byte(i%10)
		}
		buf = append(buf, fv...)
	}
	return string(buf)
}

// Each field in the message is described with a Field
type Field struct {
	Name    string
	Type    string
	Pos     int
	Length  int
	Decimal int // for decimal numbers, is the number of figures to the right of the decimal point
}

// DataObjectType returns the DataObject type of the cwf type
func (f Field) DataObjectType() string {
	switch f.Type {
	case "int":
		return "Int"
	case "decimal":
		return "Float"
	case "string":
		return "String"
	default:
		panic(fmt.Errorf("I don't know how to marshal type %v",f.Type))
	}
	return ""
}

// Source code is unicode so a rune is the fudamental token unit
type Token rune

// Tokens in the language, values start where tokens defined by text/Scanner ends
const (
	Msg = -(iota - scanner.Comment + 1)
	Op
	In
	Out
	TypeString
	TypeInt
	TypeDecimal
)

// String makes Token a Stringer
func (tok Token) String() string {
	switch tok {
	default:
		return fmt.Sprintf("%c", tok)
	case Op:
		return "operation"
	case In:
		return "in"
	case Out:
		return "out"
	case Msg:
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

// Parser is a predictive parser
type Parser struct {
	lexan        scanner.Scanner
	lookahead    Token
	curOperation Operation
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format, args...)
	os.Exit(-1)
}

// Match matches the lookahead token with tok and reads a new token/lexeme.
// It returns the value of the lookahead lexeme previous to scan the new token.
func (p *Parser) match(tok Token) string {
	if tok != p.lookahead {
		fatal("%s:fatal expecting %v\n", p.lexan.Pos(), tok)
	}
	lookaheadlex := p.lexan.TokenText()
	p.lookahead = p.scan()
	return lookaheadlex
}

// Scan advances the lookahead token, setting current a new token/lexeme
func (p *Parser) scan() Token {
	tok := p.lexan.Scan()
	switch tok {
	default:
		return Token(tok)
	case scanner.Ident:
		switch p.lexan.TokenText() {
		case "message":
			return Msg
		case "operation":
			return Op
		case "in":
			return In
		case "out":
			return Out
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

// FieldList represent the state of the parser recognizing a list of fields (left recursion eliminated)
func (p *Parser) fieldList() []*Field {
	flist := []*Field{}
	fhead := p.fieldDecl()
	fhead.Pos = 0
	flist = append(flist, fhead)
	ftail := p.restFieldDecl(fhead)
	flist = append(flist, ftail...)
	return flist
}

// FieldDecl state of the parser recognizing a single field declaration
func (p *Parser) fieldDecl() *Field {
	name := p.match(scanner.Ident)
	typ, length, dec := p.fieldType()

	return &Field{Name: name, Type: typ, Length: length, Decimal: dec}
}

// RestFieldDecl state of the parser recognizing the tail of a list of declarations (right recursive)
func (p *Parser) restFieldDecl(prevf *Field) []*Field {
	rf := []*Field{}
	pf := prevf
	for {
		if p.lookahead == Token('}') {
			return rf
		}
		f := p.fieldDecl()
		f.Pos = pf.Pos + pf.Length
		rf = append(rf, f)
		pf = f
	}
	return rf
}

// FieldType state of the parser recognizing the the type of a field in a message
func (p *Parser) fieldType() (typ string, length int, dec int) {
	switch p.lookahead {
	case TypeInt, TypeString: // int(10); | string(35);
		typ = p.match(p.lookahead)
		p.match(Token('('))
		l := p.match(scanner.Int)
		length, _ = strconv.Atoi(l)
		p.match(Token(')'))
		p.match(Token(';'))
	case TypeDecimal: // decimal(7,2);
		typ = p.match(p.lookahead)
		p.match(Token('('))
		l := p.match(scanner.Int)
		length, _ = strconv.Atoi(l)
		p.match(Token(','))
		l = p.match(scanner.Int)
		dec, _ = strconv.Atoi(l)
		p.match(Token(')'))
		p.match(Token(';'))
	default:
		fatal("%s:fatal expecting type got %s\n", p.lexan.Pos(), p.lookahead)
	}
	return
}

// Message is the definition of a message (either in or out)
func (p *Parser) message() *Message {
	msg := &Message{}
	msg.Name = p.match(scanner.Ident)
	p.match(Token('{'))
	msg.Fields = p.fieldList()
	p.match(Token('}'))

	// compute cwf's length of the message
	n := 	len(msg.Fields) -1
	if n >= 0 {
		lastf := msg.Fields[n]
		msg.Length = lastf.Pos + lastf.Length
	}
	// generate xsd
	xsdfname := msg.Name + ".xsd"
	fd,err := os.Create(xsdfname)
	if err != nil {
		fatal("creating file:%s:%v\n", xsdfname, err)
	}
	defer fd.Close()
	msg.emitXSD(fd)

	return msg
}

// Operation is the top level declaration
func (p *Parser) operation() {
	p.match(Op)
	p.curOperation.Name = p.match(scanner.Ident)
	p.match(Token('{'))
	p.match(In)
	p.match(Token(':'))
	p.curOperation.In = p.message()
	p.match(Out)
	p.match(Token(':'))
	p.curOperation.Out = p.message()
	p.match(Token('}'))
	
	// generate WSDL
	wsdlfname := p.curOperation.Name + ".wsdl"
	fd,err := os.Create(wsdlfname)
	if err != nil {
		fatal("creating file:%s:%v\n", wsdlfname, err)
	}
	defer fd.Close()
	p.curOperation.emitWSDL(fd)
	
	// generate data handler
	dhfname := p.curOperation.Name + "DH.java"
	fd,err = os.Create(dhfname)
	if err != nil {
		fatal("creating file:%s:%v\n", dhfname, err)
	}
	defer fd.Close()
	p.curOperation.emitDataHandler(fd)
}


// Compiles compiles the program read from r, and writes xsd file to w. Filename is the name bound to r (usually the name of the file in a file system)
func compile(filename string, r io.Reader, w io.Writer) {
	// Initializes the parser
	parser := &Parser{}
	parser.lexan.Init(bufio.NewReader(r))
	parser.lexan.Mode = scanner.ScanComments | scanner.SkipComments | scanner.ScanIdents | scanner.ScanInts
	parser.lexan.Position.Filename = filename
	parser.lookahead = parser.scan()
	// Start parsing calling initial state
	parser.operation()

}

func main() {
	flag.Parse()
	filename := flag.Arg(0)
	fd, err := os.Open(filename)
	if err != nil {
		fatal("opening file:%s:%v\n", filename, err)
	}
	defer fd.Close()

	/*
		xsdf := filename + ".xsd"
		fd, err := os.Create(xsdf)
		if err != nil {
			fatal("creating file:%s:%v\n", xsdf, err)
		}
		defer fd.Close()
	*/

	compile(filename, fd, os.Stdout)
}

// eq reports whether the first argument is equal to
// any of the remaining arguments.
func eq(args ...interface{}) bool {
	if len(args) == 0 {
		return false
	}
	x := args[0]
	switch x := x.(type) {
	case string, int, int64, byte, float32, float64:
		for _, y := range args[1:] {
			if x == y {
				return true
			}
		}
		return false
	}
	for _, y := range args[1:] {
		if reflect.DeepEqual(x, y) {
			return true
		}
	}
	return false
}

// lastidx returns true if i is the last index of the slice
func lastidx(slice interface{}, i int) bool {
	v := reflect.ValueOf(slice)
	kind := v.Kind()
	return (kind == reflect.Slice || kind == reflect.Array || kind == reflect.Map) && (i == v.Len()-1)
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
var wsdlTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions name="{{.Name}}" targetNamespace="http://ice.go.cr/{{.Name}}"
        xmlns:inns="http://ice.go.cr/{{.In.Name}}" xmlns:outns="http://ice.go.cr/{{.Out.Name}}"
        xmlns:tns="http://ice.go.cr/{{.Name}}" xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
        xmlns:xsd="http://www.w3.org/2001/XMLSchema">
        <wsdl:types>
                <xsd:schema targetNamespace="http://ice.go.cr/{{.Name}}">
                        <xsd:import namespace="http://ice.go.cr/{{.In.Name}}"
                                schemaLocation="{{.In.Name}}.xsd" />
                        <xsd:import namespace="http://ice.go.cr/{{.Out.Name}}"
                                schemaLocation="{{.Out.Name}}.xsd" />
                        <xsd:element name="{{.Name}}Req">
                                <xsd:complexType>
                                        <xsd:sequence>
                                                <xsd:element name="input"
                                                        type="inns:{{.In.Name}}" />
                                        </xsd:sequence>
                                </xsd:complexType>
                        </xsd:element>
                        <xsd:element name="{{.Name}}Resp">
                                <xsd:complexType>
                                        <xsd:sequence>
                                                <xsd:element name="output"
                                                        type="outns:{{.Out.Name}}" />
                                        </xsd:sequence>
                                </xsd:complexType>
                        </xsd:element>
                </xsd:schema>
        </wsdl:types>
        <wsdl:message name="{{.Name}}RequestMsg">
                <wsdl:part element="tns:{{.Name}}Req" name="{{.Name}}Parameters" />
        </wsdl:message>
        <wsdl:message name="{{.Name}}ResponseMsg">
                <wsdl:part element="tns:{{.Name}}Resp" name="{{.Name}}Result" />
        </wsdl:message>
        <wsdl:portType name="{{.Name}}">
                <wsdl:operation name="{{.Name}}Op">
                        <wsdl:input message="tns:{{.Name}}RequestMsg" name="{{.Name}}Request" />
                        <wsdl:output message="tns:{{.Name}}ResponseMsg" name="{{.Name}}Response" />
                </wsdl:operation>
        </wsdl:portType>
</wsdl:definitions>
`


var datahandlerTemplate = `
package ice;

import java.util.Map;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.ByteArrayInputStream;
import java.io.Reader;
import java.io.Writer;
import com.ibm.websphere.sca.ServiceManager;
import com.ibm.websphere.bo.BOXMLDocument;
import com.ibm.websphere.bo.BOXMLSerializer;
import commonj.sdo.DataObject;
import commonj.connector.runtime.DataHandler;
import commonj.connector.runtime.DataHandlerException;

public class {{.Name}}DH implements DataHandler {
	private Map context;

	private static final long serialVersionUID = 1314045187L;

	private static final String XMLMsgFmt = new StringBuilder()
	.append("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	.append("<object xmlns:ns2=\"http://ice.go.cr/{{.In.Name}}\" xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xsi:type=\"ns2:{{.In.Name}}\">\n")
	{{range .In.Fields}}.append("	<{{.Name}}>%s</{{.Name}}>\n")
	{{end}}.append("</object>")
	.toString();

	public static String unpackDecimal(String cwfmsg, int pos, int flen,
			int declen) {
		String intpart = cwfmsg.substring(pos, pos + flen - declen);
		String decstr = intpart + "."
				+ cwfmsg.substring(pos + flen - declen, pos + flen);

		return decstr;
	}

	public static String unpackString(String cwfmsg, int pos, int flen) {
		return cwfmsg.substring(pos, pos + flen);
	}

	public static String unpack(String cwf) {
		return String.format(XMLMsgFmt,{{$fields := .In.Fields}}{{range $i,$f := .In.Fields}}
		{{if eq $f.Type "decimal"}}unpackDecimal(cwf,{{$f.Pos}},{{$f.Length}},{{$f.Decimal}}){{else}}unpackString(cwf,{{$f.Pos}},{{$f.Length}}){{end}}{{if lastidx $fields $i|not}},{{end}}{{end}}
		);
	}

	// Transform from CWF to a DataObject
	// TODO(rolando) remove println debug statements
	public Object transform(Object source, Class target, Object options)
			throws DataHandlerException {
		if ((source == null) || (target == null))
			return null;
		if (target == DataObject.class) {
			System.out.println("CWF->DataObject");		
			if (source instanceof InputStream) {
				System.out.println("transform: InputStream->DataObject");
				byte b[] = new byte[{{.In.Length}}];
				try {
					int n = ((InputStream) source).read(b);
					if ((n > 0) && (n < b.length)) {
						throw new DataHandlerException(
								"message too short length=" + n);
					}
					String xml = unpack(new String(b));
					BOXMLSerializer xmlser = (BOXMLSerializer) ServiceManager.INSTANCE
							.locateService("com/ibm/websphere/bo/BOXMLSerializer");
					BOXMLDocument xmldoc = xmlser
							.readXMLDocument(new ByteArrayInputStream(xml
									.getBytes()));
					return xmldoc.getDataObject();
				} catch (java.io.IOException e) {
					throw new DataHandlerException(e);
				}
			} else if (source instanceof Reader) {
				System.out.println("transform: Reader->DataObject");
				char c[] = new char[{{.In.Length}}];
				try {
					int n = ((Reader) source).read(c);
					if ((n > 0) && (n < c.length)) {
						throw new DataHandlerException(
								"message too short length=" + n);
					}
					String xml = unpack(new String(c));
					System.out.println(xml);
					BOXMLSerializer xmlser = (BOXMLSerializer) ServiceManager.INSTANCE
							.locateService("com/ibm/websphere/bo/BOXMLSerializer");
					BOXMLDocument xmldoc = xmlser
							.readXMLDocument(new ByteArrayInputStream(xml
									.getBytes()));
					return xmldoc.getDataObject();
				} catch (java.io.IOException e) {
					throw new DataHandlerException(e);
				}
			}
		} else if (source instanceof DataObject) {
			System.out.println("transform: DataObject->CWF");		
			DataObject dobj = (DataObject) source;
			return String.format({{.Out.PrintfFlags}},{{$fields := .Out.Fields}}{{range $i,$f := .Out.Fields}}
					dobj.get{{$f.DataObjectType}}({{$i}}){{if lastidx $fields $i|not}},{{end}}{{end}}
					);			
		}
		throw new DataHandlerException("Transformation not supported from "
				+ source.getClass().getName() + " to "
				+ target.getClass().getName());
	}

	public void transformInto(Object source, Object target, Object options)
			throws DataHandlerException {
		if ((source == null) || (target == null))
			return;

		if (source instanceof DataObject)
			System.out.println("transformInto: DataObject->CWF");
			DataObject dobj = (DataObject) source;
			String cwf = String.format({{.Out.PrintfFlags}},{{$fields := .Out.Fields}}{{range $i,$f := .Out.Fields}}
					dobj.get{{$f.DataObjectType}}({{$i}}){{if lastidx $fields $i|not}},{{end}}{{end}}
					);			
			if (target instanceof OutputStream) {
				System.out.println("transformInto: DataObject->OutPutStream->CWF");
				OutputStream ostream = (OutputStream) target;
				try {
					ostream.write(cwf.getBytes());
					return;
				} catch(java.io.IOException e) {
					throw new DataHandlerException(e);
				}
			} else if (target instanceof Writer) {
				System.out.println("transformInto: DataObject->Writer->CWF");
				try {
					((Writer) target).write(cwf);
					return;
				} catch(java.io.IOException e) {
					throw new DataHandlerException(e);
				}
			}

			throw new DataHandlerException("Transformation not supported from "
					+ source.getClass().getName() + " to "
					+ target.getClass().getName());
	}

	public void setBindingContext(Map context) {
		// TODO(rolando) : figure out how to use this method , if applicable.
		this.context = context;
	}

}
`
