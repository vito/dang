package ast

import (
	"log"

	"github.com/dagger/dagger/codegen/introspection"
)

func CheckFile(schema *introspection.Schema, filePath string) error {
	dash, err := ParseFile(filePath)
	if err != nil {
		return err
	}

	// DISCLAIMER: i dont know wtf im doing, I'll go read a book sometime

	node := dash.(Block)

	env := NewEnv(schema)

	inferred, err := Infer(env, node, true)
	if err != nil {
		return err
	}

	log.Printf("INFERRED END: %T", inferred)

	return nil
	// return EvalReader(ctx, scope, file, source)
}

// func CheckFunctionType(fun FunDecl) error {
// 	pretty.Logln("INFERRING", fun)

// 	// Initialize the type environment
// 	env := NewRecordType("")

// 	// Add known types to the environment if any.
// 	// This might include built-in types, global variables, etc.

// 	// Infer types for function arguments and add them to the environment
// 	for _, arg := range fun.Args {
// 		env = env.Add(arg.Key, hm.NewScheme(nil, arg.Value)).(*RecordType)
// 	}

// 	// Infer the type of the function body
// 	bodyScheme, err := hm.Infer(env, fun.Form)
// 	if err != nil {
// 		return fmt.Errorf("failed to infer type for function body: %v", err)
// 	}

// 	pretty.Logln("INFERRED", bodyScheme)

// 	pretty.Logln("FINAL ENV", env)

// 	// Check against the expected return type, if any
// 	if fun.Ret != nil {
// 		bodyType, isMono := bodyScheme.Type()
// 		if !isMono {
// 			return fmt.Errorf("function body is not monomorphic")
// 		}

// 		// Check if the inferred type of the function body matches the expected return type
// 		if !bodyType.Eq(fun.Ret) {
// 			return fmt.Errorf("mismatched return type. expected: %v, got: %v", fun.Ret, bodyType)
// 		} else {
// 			pretty.Logln("RETURN TYPE MATCHES")
// 		}
// 	} else {
// 		pretty.Logln("NO RETURN TYPE")
// 	}

// 	// If everything checks out, return nil indicating no errors
// 	return nil
// }

// func EvalString(ctx context.Context, e *Scope, str string, source Readable) (Value, error) {
// 	return EvalReader(ctx, e, bytes.NewBufferString(str), source)
// }

// func EvalReader(ctx context.Context, e *Scope, r io.Reader, source Readable) (Value, error) {
// 	reader := NewReader(r, source)

// 	var res Value
// 	for {
// 		val, err := reader.Next()
// 		if err != nil {
// 			if errors.Is(err, io.EOF) {
// 				break
// 			}

// 			return nil, err
// 		}

// 		rdy := val.Eval(ctx, e, Identity)

// 		res, err = Trampoline(ctx, rdy)
// 		if err != nil {
// 			return nil, err
// 		}
// 	}

// 	return res, nil
// }
