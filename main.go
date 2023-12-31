package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/agnivade/levenshtein"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type NodeType int

var lang = typescript.GetLanguage()

type Pos struct {
	Line int
	Col  int
}

type FunctionDeclaration struct {
	Name     string
	Type     string
	Position sitter.Point
}

func (decl FunctionDeclaration) String(filename string) string {
	return fmt.Sprintf("%s:%d;  %s :: %s", filename, decl.Position.Row, decl.Name, decl.Type)
}

const (
	FUNCTION_NAME NodeType = iota
	FUNCTION_PARAMS
	FUNCTION_RETURN
	CLASS_DECL
)

func NormalizeParam(param string) string {
	return strings.TrimSpace(
		strings.Replace(param, ":", "", 1),
	)
}

func NormalizeContent(content string) string {
	trimNewlines := strings.ReplaceAll(content, "\n", "")
	reg, err := regexp.Compile("[ ]{2,}")
	if err != nil {
		log.Fatal(err)
	}
	return reg.ReplaceAllString(trimNewlines, " ")
}

func ParseFunctionTypeNode(node *sitter.Node, sourceCode []byte) string {
	params := node.ChildByFieldName("parameters")
	returnType := node.ChildByFieldName("return_type")
	strParams := ParseParamsNode(params, sourceCode)
	strReturnType := ParseTypeNode(returnType, sourceCode)

	return fmt.Sprintf("(%s) -> %s", strParams, strReturnType)
}

func ParseTypeNode(node *sitter.Node, sourceCode []byte) string {
	paramStringType := node.Type()
	if paramStringType == "type_annotation" {
		paramStringType = node.Child(1).Type()
		node = node.Child(1)
	}
	switch paramStringType {
	case "function_type", "arrow_function":
		return ParseFunctionTypeNode(node, sourceCode)
	case "type_identifier", "predefined_type", "type_annotation", "union_type", "nested_type_identifier", "array_type", "object_type", "intersection_type":
		content := node.Content(sourceCode)
		return NormalizeParam(NormalizeContent(content))

	case "readonly_type":
		return ParseTypeNode(node.NamedChild(0), sourceCode)

	case "generic_type":
		genericName := node.ChildByFieldName("name").Content(sourceCode)
		typeArgs := node.ChildByFieldName("type_arguments")
		strArgs := make([]string, 0, 4)
		for i := 0; i < int(typeArgs.ChildCount()); i++ {
			arg := typeArgs.Child(i)
			if arg.IsNamed() {
				strArgs = append(strArgs, ParseTypeNode(arg, sourceCode))
			}
		}

		return fmt.Sprintf("%s<%s>", genericName, strings.Join(strArgs, ", "))

	default:
		return "UNKNOWN: " + NormalizeContent(node.Content(sourceCode))
	}
}

func ParseParamsNode(node *sitter.Node, sourceCode []byte) string {

	formalParams := node.ChildCount()

	strParams := make([]string, 0, 4)

	for i := 0; i < int(formalParams); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "required_parameter":
			strParams = append(strParams, ParseTypeNode(child.ChildByFieldName("type"), sourceCode))
			break
		case "optional_parameter":
			strParams = append(strParams, ParseTypeNode(child.Child(2), sourceCode))
			break
		default:
			break
		}
	}

	return strings.Join(strParams, ", ")
}

func ParseFunctionNode(node *sitter.Node, sourceCode []byte) FunctionDeclaration {

	switch node.Type() {
	case "function_declaration", "function_signature":
		name := node.ChildByFieldName("name").Content(sourceCode)
		params := ParseParamsNode(node.ChildByFieldName("parameters"), sourceCode)
		returnType := ParseTypeNode(node.ChildByFieldName("return_type"), sourceCode)
		return FunctionDeclaration{
			Name:     name,
			Type:     fmt.Sprintf("(%s) -> %s", params, returnType),
			Position: node.StartPoint(),
		}

	case "variable_declarator":
		name := node.ChildByFieldName("name").Content(sourceCode)
		vType := node.ChildByFieldName("type")
		vValue := node.ChildByFieldName("value")
		var fNode *sitter.Node = nil
		if vType != nil {
			fNode = vType
		} else if vValue != nil {
			fNode = vValue
		}
		return FunctionDeclaration{
			Name:     name,
			Type:     ParseTypeNode(fNode, sourceCode),
			Position: node.StartPoint(),
		}

	case "method_definition", "method_signature":
		className := node.Parent().Parent().ChildByFieldName("name").Content(sourceCode)
		name := node.ChildByFieldName("name").Content(sourceCode)
		params := ParseParamsNode(node.ChildByFieldName("parameters"), sourceCode)
		returnType := ParseTypeNode(node.ChildByFieldName("return_type"), sourceCode)
		return FunctionDeclaration{
			Name:     className + "." + name,
			Type:     fmt.Sprintf("(%s) -> %s", params, returnType),
			Position: node.StartPoint(),
		}

	default:
		log.Fatal("Can't parse node " + node.Content(sourceCode))
		return FunctionDeclaration{}
	}
}

func ParseFile(filePath string, parser *sitter.Parser) ([]FunctionDeclaration, error) {
	cursor := sitter.NewQueryCursor()

	sourceCode, err := os.ReadFile(filePath)

	if err != nil {
		return nil, err
	}

	ast, err := parser.ParseCtx(context.Background(), nil, sourceCode)

	if err != nil {
		return nil, err
	}

	typedefsQuery := (`

  (function_declaration) @f

  (function_signature) @f
    
  (variable_declarator
    type: (type_annotation (function_type))) @f
	

  (variable_declarator
    value: (arrow_function)) @f

  (class_declaration
    (_ (method_definition) @m)
   )

  (interface_declaration
    (_ (method_signature) @m))
  `)

	query, err := sitter.NewQuery([]byte(typedefsQuery), lang)

	if err != nil {
		return nil, err
	}

	cursor.Exec(query, ast.RootNode())

	declarations := make([]FunctionDeclaration, 0, 32)

	for {
		m, ok := cursor.NextMatch()

		if !ok {
			break
		}
		m = cursor.FilterPredicates(m, sourceCode)
		captures := m.Captures

		if len(captures) == 0 {
			return []FunctionDeclaration{}, nil
		}

		node := captures[0].Node
		switch node.Type() {
		case "method_definition", "method_signature":
			declarations = append(declarations, ParseFunctionNode(node, sourceCode))
			break
		case "function_declaration", "function_signature":
			declarations = append(declarations, ParseFunctionNode(node, sourceCode))
			break
		case "variable_declarator":
			declarations = append(declarations, ParseFunctionNode(node, sourceCode))
		}

	}

	return declarations, nil
}

func ComputeDistance(declaration FunctionDeclaration, query string) int {
	return levenshtein.ComputeDistance(declaration.Type, query)
}

func main() {

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	if len(os.Args) < 3 {
		log.Fatal("You should provide file and query")
	}

	file := os.Args[1]

	declarations, err := ParseFile(file, parser)
	if err != nil {
		log.Fatal(err)
	}
	query := os.Args[2]

	sort.SliceStable(declarations, func(i, j int) bool {
		d1 := ComputeDistance(declarations[i], query)
		d2 := ComputeDistance(declarations[j], query)
		return d1 < d2
	})

	for i, d := range declarations {
		if i >= 10 {
			break
		}
		fmt.Println(d.String(file))
	}
}
