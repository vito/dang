package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/vito/dang/pkg/hm"
)

func registerAssert() {
	Builtin("assert").
		Doc("asserts that the block evaluates to a truthy value, raising an AssertionError otherwise").
		Params("message", StringType, NullValue{}).
		Block(hm.NewFnType(NewRecordType(""), TypeVar('a'))).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			if args.Block == nil {
				return nil, fmt.Errorf("assert requires a block argument")
			}
			fn := *args.Block

			result, err := callFunc(ctx, fn)
			if err != nil {
				return nil, fmt.Errorf("assert block: %w", err)
			}

			if isTruthy(result) {
				return NullValue{}, nil
			}

			msgVal, _ := args.Get("message")
			return nil, buildAssertionError(ctx, fn, msgVal)
		})
}

func buildAssertionError(ctx context.Context, fn FunctionValue, msgVal Value) error {
	body, _ := fn.Body.(*Block)
	var loc *SourceLocation
	if body != nil {
		loc = body.GetSourceLocation()
	}

	var lastExpr Node
	if body != nil && len(body.Forms) > 0 {
		lastExpr = body.Forms[len(body.Forms)-1]
	}

	var message strings.Builder
	if msg, ok := msgVal.(StringValue); ok {
		fmt.Fprintf(&message, "Assertion failed: %s\n", msg.Val)
	} else {
		message.WriteString("Assertion failed\n")
	}

	if lastExpr == nil {
		return &AssertionError{Message: "Empty assertion block", Location: loc}
	}

	fmt.Fprintf(&message, "  Expression: %s\n", assertNodeToString(lastExpr))

	closureEnv := fn.Closure
	children := assertImmediateChildren(lastExpr)
	if len(children) > 0 {
		message.WriteString("  Values:\n")
		for _, child := range children {
			if val, err := EvalNode(ctx, closureEnv, child.Node); err == nil {
				fmt.Fprintf(&message, "    %s: %s\n", child.Name, val.String())
			}
		}
	}

	return &AssertionError{
		Message:  message.String(),
		Location: lastExpr.GetSourceLocation(),
	}
}

type assertChildNode struct {
	Name string
	Node Node
}

// assertImmediateChildren extracts immediate child nodes for assertion diagnostics.
func assertImmediateChildren(expr Node) []assertChildNode {
	switch n := expr.(type) {
	case *Select:
		var children []assertChildNode
		if n.Receiver != nil {
			children = append(children, assertChildNode{Name: "receiver", Node: n.Receiver})
		}
		return children

	case *FunCall:
		var children []assertChildNode
		for i, arg := range n.Args {
			if arg.Positional {
				children = append(children, assertChildNode{Name: fmt.Sprintf("arg%d", i), Node: arg.Value})
			} else {
				children = append(children, assertChildNode{Name: arg.Key, Node: arg.Value})
			}
		}
		return children

	case *List:
		var children []assertChildNode
		for i, elem := range n.Elements {
			children = append(children, assertChildNode{Name: fmt.Sprintf("[%d]", i), Node: elem})
		}
		return children

	case *Default:
		return []assertChildNode{
			{Name: "left", Node: n.Left},
			{Name: "right", Node: n.Right},
		}

	case *Equality:
		return []assertChildNode{
			{Name: "left", Node: n.Left},
			{Name: "right", Node: n.Right},
		}

	case *Conditional:
		return []assertChildNode{
			{Name: "condition", Node: n.Condition},
		}

	case *Let:
		return []assertChildNode{
			{Name: "value", Node: n.Value},
		}
	}

	return nil
}

// assertNodeToString converts a node to its string representation for diagnostics.
func assertNodeToString(node Node) string {
	switch n := node.(type) {
	case *Symbol:
		return n.Name
	case *Select:
		if n.Receiver == nil {
			return n.Field.Name
		}
		return fmt.Sprintf("%s.%s", assertNodeToString(n.Receiver), n.Field.Name)
	case *FunCall:
		return fmt.Sprintf("%s(...)", assertNodeToString(n.Fun))
	case *String:
		return fmt.Sprintf("%q", n.Value)
	case *Int:
		return fmt.Sprintf("%d", n.Value)
	case *Boolean:
		return fmt.Sprintf("%t", n.Value)
	case *Null:
		return "null"
	case *List:
		return "[...]"
	case *Default:
		return fmt.Sprintf("%s ?? %s", assertNodeToString(n.Left), assertNodeToString(n.Right))
	case *Equality:
		return fmt.Sprintf("%s == %s", assertNodeToString(n.Left), assertNodeToString(n.Right))
	case *Conditional:
		return fmt.Sprintf("if %s { ... }", assertNodeToString(n.Condition))
	case *Let:
		return fmt.Sprintf("let %s = %s in ...", n.Name, assertNodeToString(n.Value))
	default:
		return fmt.Sprintf("%T", node)
	}
}
