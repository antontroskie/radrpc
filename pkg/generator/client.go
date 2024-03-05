package generator

import (
	"fmt"
	"go/types"

	"github.com/dave/jennifer/jen"
)

// getArgNames generates the argument names for a method.
func getArgNames(method *types.Func) *jen.Statement {
	args := jen.Empty()
	for i := 0; i < method.Type().(*types.Signature).Params().Len(); i++ {
		args.Add(jen.Id(method.Type().(*types.Signature).Params().At(i).Name())).
			Add(jen.Op(","))
	}
	return args
}

// generateMethodBody generates the body of a method.
func generateMethodBody(generatorConfig Config, method *types.Func) *jen.Statement {
	body := jen.Empty()

	returnCount := method.Type().(*types.Signature).Results().Len()

	hasReturnValue := returnCount > 0

	if returnCount > 1 {
		FailErr(fmt.Errorf("method %s has more than one return value", method.Name()))
	}

	// Return type
	returnType := jen.Empty()

	if hasReturnValue {
		returnString := method.Type().(*types.Signature).Results().At(0).Type().String()
		if RequiresQual(returnString) {
			_, sourceName := GetPathAndQualifiedName(returnString, generatorConfig)
			returnType = jen.Id(sourceName)
		} else {
			returnType = jen.Id(returnString)
		}
	}

	body.Add(
		jen.Id("config").
			Op(":=").
			Id("s").
			Dot("client").
			Dot(generatorConfig.clientConfigName).
			Op(";"),
		jen.Id("logger").Op(":=").Id("config").Dot("LoggerInterface").Op(";"),
	)

	body.Add(jen.Id("msg").Op(":=").Id("itf").Dot("RPCMessageReq").Block(
		jen.Id("Method").Op(":").Lit(method.Name()).Add(jen.Op(",")),
		jen.Id("Args").Op(":").Index().Any().Block(getArgNames(method)).Add(jen.Op(",")),
		jen.Id("Type").Op(":").Id("itf.ExecMethodRequest").Add(jen.Op(",")),
	)).Op(";").Id("res").Op(":=").Id("rdc").Dot("SendAndReceiveMessage").Call(jen.Id("s").Dot("client"), jen.Id("msg")).Op(";")

	body.Add(jen.If(jen.Id("res").Dot("ResponseError").Op("!=").Lit("")).Block(
		jen.Id("logger").
			Dot("LogError").
			Call(jen.Qual("fmt", "Errorf").Call(jen.Lit("error executing method: %v"), jen.Id("res").Dot("ResponseError"))),
	),
	).Op(";")

	if hasReturnValue {
		defaultVal, err := DefaultValueAsString(
			method.Type().(*types.Signature).Results().At(0).Type(),
			generatorConfig,
		)
		if err != nil {
			FailErr(err)
		}
		body.Add(jen.If(jen.Id("res").Dot("ResponseSuccess").Op("==").Nil()).Block(
			jen.Id("logger").
				Dot("LogError").
				Call(jen.Qual("fmt", "Errorf").Call(jen.Lit("received %v response from request with ID: %v"), jen.Id("res").Dot("ResponseSuccess"), jen.Id("res").Dot("ID"))),
			jen.Return(jen.Id(defaultVal)),
		),
		).Op(";")
	}

	if hasReturnValue {
		body.Add(
			jen.Id("response").
				Op(",").
				Id("ok").
				Op(":=").
				Id("res").
				Dot("ResponseSuccess").
				Assert(returnType).
				Op(";").If(jen.Op("!").Id("ok")).Block(
				jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("error type asserting response: %v"), jen.Id("res").Dot("ResponseSuccess"))),
			),
		).Op(";").Return(jen.Id("response"))
	}

	return body
}

// getFunctionReturns generates the return types for a method.
func getFunctionReturns(generatorConfig Config, method *types.Func) *jen.Statement {
	returns := method.Type().(*types.Signature).Results()
	params := jen.Empty()
	for i := 0; i < returns.Len(); i++ {
		param := returns.At(i).Type().String()
		if RequiresQual(param) {
			_, sourceName := GetPathAndQualifiedName(param, generatorConfig)
			params.Add(jen.Id(sourceName))
		} else {
			params.Add(jen.Id(param))
		}
		if i < returns.Len()-1 {
			params.Add(jen.Op(","))
		}
	}
	return params
}

// getFunctionParams generates the parameters for a method.
func getFunctionParams(generatorConfig Config, method *types.Func) *jen.Statement {
	params := jen.Empty()
	for i := 0; i < method.Type().(*types.Signature).Params().Len(); i++ {
		param := method.Type().(*types.Signature).Params().At(i)
		paramType := param.Type().String()
		if RequiresQual(paramType) {
			_, sourceName := GetPathAndQualifiedName(paramType, generatorConfig)
			params.Add(jen.Id(param.Name()).Id(sourceName))
		} else {
			params.Add(jen.Id(param.Name()).Id(paramType))
		}
		if i < method.Type().(*types.Signature).Params().Len()-1 {
			params.Add(jen.Op(","))
		}
	}
	return params
}

// generateMethod generates an RPC method for the client.
func generateMethod(generatorConfig Config, method *types.Func) *jen.Statement {
	return jen.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.clientStructName)).
		Id(method.Name()).
		Params(getFunctionParams(generatorConfig, method)).
		Params(getFunctionReturns(generatorConfig, method)).
		Block(generateMethodBody(generatorConfig, method))
}

// GenerateClientMethods generates the RPC methods for the client.
func GenerateClientMethods(
	generatorConfig Config,
) jen.Code {
	// Create a new jen statement
	stmt := jen.Empty()

	// Get all interface methods
	for i := 0; i < generatorConfig.initConfig.InterfaceType.NumMethods(); i++ {
		method := generatorConfig.initConfig.InterfaceType.Method(i)
		stmt.Add(generateMethod(generatorConfig, method))
		stmt.Add(jen.Op(";"))
	}

	stmt.Add(jen.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("GetConn").Params().Id("net.Conn").Block(
		jen.Id("s").Dot(generatorConfig.clientMutexName).Dot("RLock").Call().Op(";"),
		jen.Defer().Id("s").Dot(generatorConfig.clientMutexName).Dot("RUnlock").Call().Op(";"),
		jen.Return(jen.Id("s").Dot(generatorConfig.clientConnsName)),
	).Op(";"))

	stmt.Add(jen.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("SetConn").Params(jen.Id("conn").Id("net.Conn")).Block(
		jen.Id("s").Dot(generatorConfig.clientMutexName).Dot("Lock").Call().Op(";"),
		jen.Defer().Id("s").Dot(generatorConfig.clientMutexName).Dot("Unlock").Call().Op(";"),
		jen.Id("s").Dot(generatorConfig.clientConnsName).Op("=").Id("conn"),
	).Op(";"))

	stmt.Add(jen.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("GetConfig").Params().Id("rdc.RPCClientConfig").Block(
		jen.Id("s").Dot(generatorConfig.clientMutexName).Dot("RLock").Call().Op(";"),
		jen.Defer().Id("s").Dot(generatorConfig.clientMutexName).Dot("RUnlock").Call().Op(";"),
		jen.Return(jen.Id("s").Dot(generatorConfig.clientConfigName)),
	).Op(";"))

	stmt.Add(jen.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("SetConfig").Params(jen.Id("config").Id("rdc.RPCClientConfig")).Block(
		jen.Id("s").Dot(generatorConfig.clientMutexName).Dot("Lock").Call().Op(";"),
		jen.Defer().Id("s").Dot(generatorConfig.clientMutexName).Dot("Unlock").Call().Op(";"),
		jen.Id("s").Dot(generatorConfig.clientConfigName).Op("=").Id("config"),
	).Op(";"))

	stmt.Add(jen.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("GetMessagePool").Params().Id("rdc.RPCClientMessagePool").Block(
		jen.Id("s").Dot(generatorConfig.clientMutexName).Dot("RLock").Call().Op(";"),
		jen.Defer().Id("s").Dot(generatorConfig.clientMutexName).Dot("RUnlock").Call().Op(";"),
		jen.Return(jen.Id("s").Dot(generatorConfig.clientMessagePoolName)),
	).Op(";"))

	stmt.Add(jen.Func().Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("SetMessagePool").Params(jen.Id("config").Id("rdc.RPCClientMessagePool")).Block(
		jen.Id("s").Dot(generatorConfig.clientMutexName).Dot("Lock").Call().Op(";"),
		jen.Defer().Id("s").Dot(generatorConfig.clientMutexName).Dot("Unlock").Call().Op(";"),
		jen.Id("s").Dot(generatorConfig.clientMessagePoolName).Op("=").Id("config"),
	).Op(";"))

	return stmt
}

// GenerateClientConAndDisconnMethod generates the connect and disconnect methods for the client.
func GenerateClientConAndDisconnMethod(
	generatorConfig Config,
) jen.Code {
	// Create a new jen statement
	stmt := jen.Empty()

	// Add the method signature
	stmt.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("Connect").
		Params(jen.Id("config").Id("rdc.RPCClientConfig")).
		Params(jen.Op("*").Id(generatorConfig.clientStructName), jen.Id("error")).
		Block(
			jen.Id("s").Dot("SetConfig").Call(jen.Id("config")).Op(";"),
			jen.Add(getGobRegisters(generatorConfig)).Op(";"),
			jen.Id("interf").Op(":=").Op("&").Id(generatorConfig.clientStructName).Block(
				jen.Id("client").Op(":").Id("s").Op(","),
			).Op(";"),
			jen.Return(jen.Id("interf"), jen.Id("rdc").Dot("Connect").Params(jen.Id("s"))),
		).
		Op(";")

	// Add the method signature
	stmt.Func().
		Params(jen.Id("s").Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Id("Disconnect").
		Params().
		Params(jen.Id("error")).
		Block(
			jen.Return(jen.Id("rdc").Dot("Disconnect").Call(jen.Id("s")).Op(";")),
		).
		Op(";")

	return stmt
}
