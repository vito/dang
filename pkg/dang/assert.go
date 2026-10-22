package dang

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

func registerAssert() {
	Builtin("assert").
		Doc("asserts that the block evaluates to a truthy value, raising an AssertionError otherwise").
		Example(`assert { 1 + 1 == 2 }`).
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

	// Child values are obtained by re-evaluating the child nodes in the
	// block's closure scope, so failure diagnostics re-run sub-expressions.
	// Names bound earlier in the block don't exist there (or would resolve to
	// an outer binding they shadow), so nodes referencing them are reported
	// as unavailable instead.
	diag := &assertDiag{ctx: ctx, scope: fn.Closure, locals: map[string]bool{}}
	for _, form := range body.Forms[:len(body.Forms)-1] {
		for _, name := range form.DeclaredSymbols() {
			diag.locals[name] = true
		}
	}
	var values strings.Builder
	failed := false
	diag.writeValues(&values, lastExpr, &failed, "    ")
	if values.Len() > 0 {
		message.WriteString("  Values:\n")
		message.WriteString(values.String())
	}

	return &AssertionError{
		Message:  message.String(),
		Location: lastExpr.GetSourceLocation(),
	}
}

type assertDiag struct {
	ctx    context.Context
	scope  ValueScope
	locals map[string]bool
}

// eval re-evaluates node for diagnostics, reporting false if its value is
// unavailable.
func (d *assertDiag) eval(node Node) (Value, bool) {
	for _, name := range node.ReferencedSymbols() {
		if d.locals[name] {
			return nil, false
		}
	}
	val, err := EvalNode(d.ctx, d.scope, node)
	return val, err == nil
}

// writeValues writes one "name: value" line per interesting child of
// expr. Operands of logical operators (and, or, !) are named by their source
// expression and have their own children listed beneath them, indented, so
// a failing `a.contains("x") and b == 2` shows which side was false and why.
// known is expr's truthiness when the assertion ran, if known.
func (d *assertDiag) writeValues(w *strings.Builder, expr Node, known *bool, indent string) {
	for {
		g, ok := expr.(*Grouped)
		if !ok {
			break
		}
		expr = g.Expr
	}

	if operands, stopOn, ok := assertLogicalOperands(expr); ok {
		d.writeOperands(w, operands, stopOn, known, indent)
		return
	}

	for _, child := range assertImmediateChildren(expr) {
		if assertIsLiteral(child.Node) {
			// Already spelled out in the expression.
			continue
		}
		if val, ok := d.eval(child.Node); ok {
			fmt.Fprintf(w, "%s%s: %s\n", indent, child.Name, Repr(val))
		} else {
			fmt.Fprintf(w, "%s%s: (unavailable)\n", indent, child.Name)
		}
	}
}

// writeOperands writes the operands of a logical operator, mirroring its
// short-circuit evaluation: once an operand's truthiness equals stopOn, the
// remaining operands are reported as not evaluated rather than run.
//
// known is the truthiness the whole expression had when the assertion ran,
// if known (the asserted expression itself was falsy). A falsy `or` or a
// truthy `and` cannot have short-circuited, so all of its operands ran; this
// keeps the report accurate when an operand can't be re-evaluated, e.g.
// because it refers to a `let` local to the assert block.
func (d *assertDiag) writeOperands(w *strings.Builder, operands []Node, stopOn *bool, known *bool, indent string) {
	allRan := stopOn == nil || (known != nil && *known != *stopOn)
	stopped, unknown := false, false
	for _, operand := range operands {
		name := assertNodeToString(operand)
		literal := assertIsLiteral(operand)
		if stopped || unknown {
			if !literal {
				status := "(not evaluated)"
				if unknown {
					status = "(unavailable)"
				}
				fmt.Fprintf(w, "%s%s: %s\n", indent, name, status)
			}
			continue
		}
		val, ok := d.eval(operand)
		if !ok {
			fmt.Fprintf(w, "%s%s: (unavailable)\n", indent, name)
			// Without its value there's no telling whether the operator
			// short-circuited here, unless every operand is known to have run.
			unknown = !allRan
			continue
		}
		truthy := isTruthy(val)
		if !literal {
			fmt.Fprintf(w, "%s%s: %s\n", indent, name, Repr(val))
			// Only break down operands that explain the result (the false
			// side of a failed `and`), keeping output and re-runs down.
			if stopOn == nil || known == nil || truthy == *known {
				d.writeValues(w, operand, &truthy, indent+"  ")
			}
		}
		if !allRan && truthy == *stopOn {
			stopped = true
		}
	}
}

// assertLogicalOperands returns the operands of a logical operator, with
// chains of the same operator (a and b and c) flattened. stopOn is the
// truthiness at which evaluation short-circuits, or nil for `!`.
func assertLogicalOperands(expr Node) (operands []Node, stopOn *bool, ok bool) {
	stopOnFalse, stopOnTrue := false, true
	switch n := expr.(type) {
	case *LogicalAnd:
		return flattenAssertLogical(n), &stopOnFalse, true
	case *LogicalOr:
		return flattenAssertLogical(n), &stopOnTrue, true
	case *UnaryNegation:
		return []Node{n.Expr}, nil, true
	}
	return nil, nil, false
}

func flattenAssertLogical(expr Node) []Node {
	switch n := expr.(type) {
	case *LogicalAnd:
		if _, ok := n.Left.(*LogicalAnd); ok {
			return append(flattenAssertLogical(n.Left), n.Right)
		}
		return []Node{n.Left, n.Right}
	case *LogicalOr:
		if _, ok := n.Left.(*LogicalOr); ok {
			return append(flattenAssertLogical(n.Left), n.Right)
		}
		return []Node{n.Left, n.Right}
	}
	return []Node{expr}
}

// assertIsLiteral reports whether node is a literal whose value is already
// visible in the rendered expression.
func assertIsLiteral(node Node) bool {
	for {
		c, ok := node.(*Coerce)
		if !ok {
			break
		}
		node = c.Expr
	}
	switch node.(type) {
	case *String, *Int, *Float, *Boolean, *Null:
		return true
	}
	return false
}

type assertChildNode struct {
	Name string
	Node Node
}

// assertImmediateChildren extracts immediate child nodes for assertion diagnostics.
func assertImmediateChildren(expr Node) []assertChildNode {
	if _, left, right, ok := assertBinaryOperator(expr); ok {
		return []assertChildNode{
			{Name: "left", Node: left},
			{Name: "right", Node: right},
		}
	}

	switch n := expr.(type) {
	case *Grouped:
		return assertImmediateChildren(n.Expr)

	case *Select:
		var children []assertChildNode
		if recv := assertReceiver(n); recv != nil {
			children = append(children, assertChildNode{Name: "receiver", Node: recv})
		}
		return children

	case *FunCall:
		var children []assertChildNode
		if sel, ok := n.Fun.(*Select); ok {
			if recv := assertReceiver(sel); recv != nil {
				children = append(children, assertChildNode{Name: "receiver", Node: recv})
			}
		}
		for i, arg := range n.Args {
			if arg.Positional {
				children = append(children, assertChildNode{Name: fmt.Sprintf("arg%d", i), Node: arg.Value})
			} else {
				children = append(children, assertChildNode{Name: arg.Key, Node: arg.Value})
			}
		}
		return children

	case *Index:
		return []assertChildNode{
			{Name: "receiver", Node: n.Receiver},
			{Name: "index", Node: n.Index},
		}

	case *List:
		var children []assertChildNode
		for i, elem := range n.Elements {
			children = append(children, assertChildNode{Name: fmt.Sprintf("[%d]", i), Node: elem})
		}
		return children

	case *Conditional:
		return []assertChildNode{
			{Name: "condition", Node: n.Condition},
		}
	}

	return nil
}

// assertReceiver returns the receiver of a selection worth showing, if any.
// Static members (e.g. Regexp.escape) are skipped: their receiver names a
// type, and evaluating it would construct an instance.
func assertReceiver(sel *Select) Node {
	if sel.Receiver == nil || sel.staticOwner != nil {
		return nil
	}
	return sel.Receiver
}

// assertBinaryOperator destructures infix operators into their source
// operator and operands.
func assertBinaryOperator(node Node) (op string, left, right Node, ok bool) {
	var bin *BinaryOperator
	switch n := node.(type) {
	case *Equality:
		return "==", n.Left, n.Right, true
	case *Default:
		return "??", n.Left, n.Right, true
	case *LogicalAnd:
		return "and", n.Left, n.Right, true
	case *LogicalOr:
		return "or", n.Left, n.Right, true
	case *Inequality:
		op, bin = "!=", &n.BinaryOperator
	case *LessThan:
		op, bin = "<", &n.BinaryOperator
	case *LessThanEqual:
		op, bin = "<=", &n.BinaryOperator
	case *GreaterThan:
		op, bin = ">", &n.BinaryOperator
	case *GreaterThanEqual:
		op, bin = ">=", &n.BinaryOperator
	case *Addition:
		op, bin = "+", &n.BinaryOperator
	case *Subtraction:
		op, bin = "-", &n.BinaryOperator
	case *Multiplication:
		op, bin = "*", &n.BinaryOperator
	case *Division:
		op, bin = "/", &n.BinaryOperator
	case *Modulo:
		op, bin = "%", &n.BinaryOperator
	default:
		return "", nil, nil, false
	}
	return op, bin.Left, bin.Right, true
}

// assertNodeToString converts a node to its string representation for diagnostics.
func assertNodeToString(node Node) string {
	if s, ok := assertNodeString(node); ok {
		return s
	}
	return fmt.Sprintf("%T", node)
}

// assertSubNodeToString renders a nested node, eliding ones it can't render.
func assertSubNodeToString(node Node) string {
	if s, ok := assertNodeString(node); ok {
		return s
	}
	return "..."
}

func assertNodeString(node Node) (string, bool) {
	if op, left, right, ok := assertBinaryOperator(node); ok {
		return fmt.Sprintf("%s %s %s", assertSubNodeToString(left), op, assertSubNodeToString(right)), true
	}

	switch n := node.(type) {
	case *Coerce:
		// Inserted by inference at typed boundaries such as call arguments.
		return assertNodeString(n.Expr)
	case *Symbol:
		return n.Name, true
	case *SelfKeyword:
		return "self", true
	case *Select:
		if n.Receiver == nil {
			return n.Field.Name, true
		}
		return fmt.Sprintf("%s.%s", assertSubNodeToString(n.Receiver), n.Field.Name), true
	case *FunCall:
		args := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			if arg.Positional {
				args = append(args, assertSubNodeToString(arg.Value))
			} else {
				args = append(args, fmt.Sprintf("%s: %s", arg.Key, assertSubNodeToString(arg.Value)))
			}
		}
		fun := assertSubNodeToString(n.Fun)
		var s string
		if len(args) > 0 || n.BlockArg == nil {
			s = fmt.Sprintf("%s(%s)", fun, strings.Join(args, ", "))
		} else {
			s = fun
		}
		if n.BlockArg != nil {
			s += " { ... }"
		}
		return s, true
	case *Index:
		return fmt.Sprintf("%s[%s]", assertSubNodeToString(n.Receiver), assertSubNodeToString(n.Index)), true
	case *Grouped:
		return fmt.Sprintf("(%s)", assertSubNodeToString(n.Expr)), true
	case *UnaryNegation:
		return "!" + assertSubNodeToString(n.Expr), true
	case *UnaryMinus:
		return "-" + assertSubNodeToString(n.Expr), true
	case *NonNullAssert:
		return assertSubNodeToString(n.Expr) + "!", true
	case *String:
		return strconv.Quote(n.Value), true
	case *Template:
		if n.Fence != 1 {
			return "`...`", true
		}
		var b strings.Builder
		b.WriteString("`")
		for _, part := range n.Parts {
			if part.Expr != nil {
				fmt.Fprintf(&b, "${%s}", assertSubNodeToString(part.Expr))
			} else {
				b.WriteString(part.Lit)
			}
		}
		b.WriteString("`")
		return b.String(), true
	case *Int:
		return fmt.Sprintf("%d", n.Value), true
	case *Float:
		if n.Text != "" {
			return n.Text, true
		}
		return strconv.FormatFloat(n.Value, 'g', -1, 64), true
	case *Boolean:
		return fmt.Sprintf("%t", n.Value), true
	case *Null:
		return "null", true
	case *List:
		return "[...]", true
	case *Conditional:
		return fmt.Sprintf("if %s { ... }", assertSubNodeToString(n.Condition)), true
	}
	return "", false
}
