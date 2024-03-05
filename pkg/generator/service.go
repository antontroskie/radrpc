package generator

import (
	"fmt"
	"go/types"

	"github.com/dave/jennifer/jen"
)

// Generate the start and stop service methods.
func GenerateStartAndStopServiceMethod(
	generatorConfig Config,
) jen.Code {
	// Create a new jen statement
	stmt := jen.Empty()

	stmt.Add(
		jen.Func().
			Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
			Id("StartService").
			Params(jen.Id("m").Id(generatorConfig.serviceUnderlyingInterfaceType),
				jen.Id("config").Id("rds.RPCServiceConfig")).
			Params(jen.Id("error")).Block(
			jen.Add(getGobRegisters(generatorConfig)),
			jen.Id("s").Dot("SetConfig").Call(jen.Id("config")).Op(";"),
			jen.Id("s").
				Dot(generatorConfig.serviceUnderlyingInterfaceName).
				Op("=").
				Id("m").
				Op(";"),
			jen.Return(jen.Id("rds").
				Dot("StartService").
				Call(jen.Id("s"), jen.Id("s.parseRPCMessage")),
			)),
	).Op(";")

	stmt.Add(
		jen.Func().
			Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
			Id("StopService").
			Params().Params(jen.Error()).
			Block(jen.Return().Id("rds").
				Dot("StopService").
				Call(jen.Id("s")),
			),
	).Op(";")

	return stmt
}

// GenerateServiceMethodsSwitchCase generates the switch cases to handle different message types.
func GenerateServiceMethodsSwitchCase(
	config Config,
	method *types.Func,
	stmt *jen.Statement,
) {
	// Get the method name
	methodName := method.Name()

	// Method arguments
	args := method.Type().(*types.Signature).Params()

	// Arguments length
	argCount := args.Len()

	// Method return value
	returns := method.Type().(*types.Signature).Results()

	// Is return value non-void
	hasReturnValue := returns.Len() > 0

	argTypes := make([]*jen.Statement, argCount)
	for i := 0; i < argCount; i++ {
		if RequiresQual(args.At(i).Type().String()) {
			_, sourceTypeName := GetPathAndQualifiedName(args.At(i).Type().String(), config)
			argTypes[i] = jen.Id(sourceTypeName)
			continue
		}
		argTypes[i] = jen.Id(args.At(i).Type().String())
	}

	getArgDeclarations := func(argCount int) *jen.Statement {
		decls := jen.Empty()
		for i := 0; i < argCount; i++ {
			decls.Add(
				jen.Op(";"),
				jen.Id("arg"+fmt.Sprint(i)).
					Op(",").
					Id("ok").
					Op(":=").
					Id("args").
					Index(jen.Lit(i)).
					Assert(argTypes[i]),
				jen.Op(";"),
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Id("itf.RPCMessageRes").Block(
						jen.Id("ID").Op(":").Id("id").Add(jen.Op(",")),
						jen.Id("ResponseError").Op(":").Qual("fmt", "Sprintf").Call(
							jen.Lit(methodName+": expected argument "+fmt.Sprint(i)+" to be of type %T, got %T"),
							jen.Add(jen.Op(`"`), jen.Add(argTypes[i])).Op(`"`),
							jen.Id("args").Index(jen.Lit(i)),
						).
							Add(jen.Op(","))))),
			)
		}
		return decls
	}

	getNoRetCallStructure := func(methodName string, argCount int) *jen.Statement {
		arguments := make([]jen.Code, argCount)
		for i := 0; i < argCount; i++ {
			arguments[i] = jen.Id("arg" + fmt.Sprint(i))
		}
		callStructure := jen.Id("s").Dot(config.serviceUnderlyingInterfaceName).
			Dot(methodName).Call(arguments...)
		return callStructure
	}

	getRetCallStructure := func(methodName string, argCount int) *jen.Statement {
		arguments := make([]jen.Code, argCount)
		for i := 0; i < argCount; i++ {
			arguments[i] = jen.Id("arg" + fmt.Sprint(i))
		}
		fn := jen.Func().Params()
		// Add return types to the function signature
		for i := 0; i < returns.Len(); i++ {
			returnType := returns.At(i).Type()
			returnTypeString := returnType.String()
			if RequiresQual(returnTypeString) {
				_, sourceName := GetPathAndQualifiedName(returnTypeString, config)
				fn = fn.Params(jen.Id(sourceName))
			} else {
				fn = fn.Params(jen.Id(returnType.String()))
			}
		}
		callStructure := jen.Id("s").
			Dot(config.serviceUnderlyingInterfaceName).
			Dot(methodName).
			Call(arguments...)
		return callStructure
	}
	stmt.Add(jen.Case(jen.Lit(methodName)).Block(
		jen.Id("argCount").Op(":=").Lit(argCount),
		jen.If(jen.Len(jen.Id("msg").Dot("Args")).Op("!=").Id("argCount")).Block(
			jen.Return(jen.Id("itf.RPCMessageRes").Block(
				jen.Id("ID").Op(":").Id("id").Add(jen.Op(",")),
				jen.Id("ResponseError").Op(":").Qual("fmt", "Sprintf").Call(
					jen.Lit(methodName+": expected %d arguments, got %d"),
					jen.Id("argCount"),
					jen.Len(jen.Id("msg").Dot("Args")),
				).Add(jen.Op(",")),
			)),
		)),
		jen.Op(";"))

	if argCount > 0 {
		stmt.Add(jen.Id("args").Op(":=").Id("msg").Dot("Args"),
			getArgDeclarations(argCount),
			jen.Op(";"))
	} else {
		stmt.Add(jen.Op(";"))
	}

	if hasReturnValue {
		stmt.Add(
			jen.Id("s").Dot(config.serviceMutexName).Dot("Lock").Call().Op(";"),
			jen.Defer().Id("s").Dot(config.serviceMutexName).Dot("Unlock").Call().Op(";"),
			jen.Id("returnVal").
				Op(":=").
				Add(getRetCallStructure(methodName, argCount)),
			jen.Op(";"),
			jen.Return(jen.Id("itf.RPCMessageRes").Block(
				jen.Id("ID").Op(":").Id("id").Add(jen.Op(",")),
				jen.Id("ResponseSuccess").Op(":").Id("returnVal").Add(jen.Op(",")),
			)),
		)
	} else {
		stmt.Add(
			jen.Id("s").Dot(config.serviceMutexName).Dot("Lock").Call().Op(";"),
			jen.Defer().Id("s").Dot(config.serviceMutexName).Dot("Unlock").Call().Op(";"),
			getNoRetCallStructure(methodName, argCount),
			jen.Op(";"),
			jen.Return(jen.Id("itf.RPCMessageRes").Block(
				jen.Id("ID").Op(":").Id("id").Add(jen.Op(",")),
			)),
		)
	}
}

func GenerateServiceMethods(
	generatorConfig Config,
) jen.Code {
	// Create a new jen statement
	stmt := jen.Empty()

	// Add the method signature
	stmt.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Id("parseRPCMessage").
		Params(
			jen.Id("msg").Id("itf.RPCMessageReq"),
		).
		Id("itf.RPCMessageRes")

	cases := jen.Empty()
	// Get all interface methods
	for i := 0; i < generatorConfig.initConfig.InterfaceType.NumMethods(); i++ {
		method := generatorConfig.initConfig.InterfaceType.Method(i)
		GenerateServiceMethodsSwitchCase(
			generatorConfig,
			method,
			cases,
		)
		cases.Add(jen.Op(";"))
	}

	// Add the method body
	stmt.Block(
		jen.Id("id").Op(":=").Id("msg").Dot("ID"),
		jen.Switch(jen.Id("msg").Dot("Method")).Block(
			cases,
			jen.Default().Block(
				jen.Return(jen.Id("itf.RPCMessageRes").Block(
					jen.Id("ID").Op(":").Id("id").Add(jen.Op(",")),
					jen.Id("ResponseError").Op(":").Qual("fmt", "Sprintf").Call(
						jen.Lit("unknown method: %s"),
						jen.Id("msg").Dot("Method"),
					).Add(jen.Op(",")),
				)),
			),
		),
	)

	stmt.Add(jen.Op(";")).
		Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Id("GetConns").
		Params().
		Index().
		Id("net.Conn").
		Block(
			jen.Id("s").Dot(generatorConfig.serviceMutexName).Dot("RLock").Call().Op(";"),
			jen.Defer().Id("s").Dot(generatorConfig.serviceMutexName).Dot("RUnlock").Call().Op(";"),
			jen.Return(jen.Id("s").Dot(generatorConfig.serviceConnsName)),
		).
		Op(";")

	stmt.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Id("SetConns").
		Params(jen.Id("conns").Index().Id("net.Conn")).
		Block(
			jen.Id("s").Dot(generatorConfig.serviceMutexName).Dot("Lock").Call().Op(";"),
			jen.Defer().Id("s").Dot(generatorConfig.serviceMutexName).Dot("Unlock").Call().Op(";"),
			jen.Id("s").Dot(generatorConfig.serviceConnsName).Op("=").Id("conns"),
		).
		Op(";")

	stmt.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Id("SetNewConn").
		Params(jen.Id("conn").Id("net.Conn")).
		Block(
			jen.Id("s").Dot(generatorConfig.serviceMutexName).Dot("Lock").Call().Op(";"),
			jen.Defer().Id("s").Dot(generatorConfig.serviceMutexName).Dot("Unlock").Call().Op(";"),
			jen.Id("s").
				Dot(generatorConfig.serviceConnsName).
				Op("=").
				Append(jen.Id("s").Dot(generatorConfig.serviceConnsName), jen.Id("conn")),
		).
		Op(";")

	stmt.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Id("GetConfig").
		Params().
		Params(jen.Id("rds.RPCServiceConfig")).
		Block(
			jen.Id("s").Dot(generatorConfig.serviceMutexName).Dot("RLock").Call().Op(";"),
			jen.Defer().Id("s").Dot(generatorConfig.serviceMutexName).Dot("RUnlock").Call().Op(";"),
			jen.Return(jen.Id("s").Dot(generatorConfig.serviceConfigName)),
		).
		Op(";")

	stmt.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Id("SetConfig").
		Params(jen.Id("config").Id("rds.RPCServiceConfig")).
		Block(
			jen.Id("s").Dot(generatorConfig.serviceMutexName).Dot("Lock").Call().Op(";"),
			jen.Defer().Id("s").Dot(generatorConfig.serviceMutexName).Dot("Unlock").Call().Op(";"),
			jen.Id("s").Dot(generatorConfig.serviceConfigName).Op("=").Id("config"),
		).
		Op(";")

	return stmt
}
