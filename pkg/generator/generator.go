package generator

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/dave/jennifer/jen"
)

type InitConfig struct {
	ServiceStructName          string
	ClientStructName           string
	InterfaceType              *types.Interface
	Implementation             types.Type
	ImplementationPackageName  string
	ImplementationName         string
	ImplementationRelativePath string
	ImplementationPackage      string
	ImplementationDirectory    string
}

type Config struct {
	initConfig                     InitConfig
	gobTypes                       map[string]string
	mainStructName                 string
	lowercaseServiceStructName     string
	lowercaseClientStructName      string
	serviceStructName              string
	clientStructName               string
	serviceConnsName               string
	serviceConfigName              string
	serviceMutexName               string
	clientConnsName                string
	clientMutexName                string
	clientConfigName               string
	clientMessagePoolName          string
	serviceUnderlyingInterfaceType string
	serviceUnderlyingInterfaceName string
}

func GenerateMainStruct(
	generatorConfig Config,
) jen.Code {
	stmt := jen.Empty()

	stmt.Type().Id(generatorConfig.lowercaseServiceStructName).
		Struct(jen.Id(generatorConfig.serviceConnsName).
			Index().Qual("net", "Conn"),
			jen.Id(generatorConfig.serviceConfigName).Id("rds.RPCServiceConfig"),
			jen.Id(generatorConfig.serviceMutexName).Op("*").Qual("sync", "RWMutex"),
			jen.Id(generatorConfig.serviceUnderlyingInterfaceName).Id(
				generatorConfig.serviceUnderlyingInterfaceType,
			),
		).
		Op(";")

	stmt.Type().Id(generatorConfig.lowercaseClientStructName).Struct(
		jen.Id(generatorConfig.clientConnsName).Qual("net", "Conn"),
		jen.Id(generatorConfig.clientMutexName).Op("*").Qual("sync", "RWMutex"),
		jen.Id(generatorConfig.clientConfigName).Id("rdc.RPCClientConfig"),
		jen.Id(generatorConfig.clientMessagePoolName).Id("rdc.RPCClientMessagePool"),
	).Op(";")

	stmt.Type().Id(generatorConfig.serviceStructName).Struct(
		jen.Id("service").Op("*").Id(generatorConfig.lowercaseServiceStructName),
	).Op(";")

	stmt.Type().Id(generatorConfig.clientStructName).Struct(
		jen.Id("client").Op("*").Id(generatorConfig.lowercaseClientStructName),
	).Op(";")

	stmt.Func().
		Id("New" + generatorConfig.mainStructName + "Service").
		Params().
		Params(jen.Op("*").Id(generatorConfig.lowercaseServiceStructName)).
		Block(
			jen.Return(jen.Op("&").Id(generatorConfig.lowercaseServiceStructName).Block(
				jen.Id(generatorConfig.serviceMutexName).
					Op(":").
					New(jen.Id("sync").Dot("RWMutex")).
					Op(","),
			),
			),
		).
		Add(jen.Op(";"))

	stmt.Func().
		Id("New" + generatorConfig.mainStructName + "Client").
		Params().
		Params(jen.Op("*").Id(generatorConfig.lowercaseClientStructName)).
		Block(
			jen.Return(jen.Op("&").Id(generatorConfig.lowercaseClientStructName).Block(
				jen.Id(generatorConfig.clientMutexName).
					Op(":").
					New(jen.Id("sync").Dot("RWMutex")).
					Op(","),
			),
			),
		).
		Add(jen.Op(";"))

	return stmt
}

func getGobRegisters(config Config) *jen.Statement {
	gobDeclarations := jen.Empty()
	for gobType := range config.gobTypes {
		gobDeclarations.Add(jen.Id("gob").Dot("Register").Call(jen.Id(gobType).Block())).Op(";")
	}
	return gobDeclarations
}

func GenerateForInterface(
	config InitConfig,
) error {
	// Create a new file
	f := jen.NewFile(config.ImplementationPackageName + "rad")

	// Struct name
	serviceStructName := config.ImplementationName + "RDS"
	clientStructName := config.ImplementationName + "RDC"

	// Assign to config
	config.ServiceStructName = serviceStructName
	config.ClientStructName = clientStructName

	// Get required imports
	itfImportPath := "github.com/antontroskie/radrpc/pkg/rpc/itf"
	radRPCClient := "github.com/antontroskie/radrpc/pkg/rpc/client"
	radRPCService := "github.com/antontroskie/radrpc/pkg/rpc/service"

	// Struct and member names
	lowercaseServiceStructName := strings.ToLower(serviceStructName[:1]) + serviceStructName[1:]
	lowercaseClientStructName := strings.ToLower(clientStructName[:1]) + clientStructName[1:]
	serviceUnderlyingInterfaceType := config.ImplementationPackageName + "." + config.ImplementationName
	serviceUnderlyingInterfaceName := lowercaseServiceStructName + "Interf"
	serviceConnsName := lowercaseServiceStructName + "Conns"
	serviceConfigName := lowercaseServiceStructName + "Config"
	serviceMutexName := lowercaseServiceStructName + "Mutex"
	clientConfigName := lowercaseClientStructName + "Config"
	clientConnsName := lowercaseClientStructName + "Conn"
	clientMutexName := lowercaseClientStructName + "Mutex"
	clientMessagePoolName := lowercaseClientStructName + "MessagePool"
	mainStructName := config.ImplementationName + "RD"

	generatorConfig := Config{
		initConfig:                     config,
		gobTypes:                       make(map[string]string),
		mainStructName:                 mainStructName,
		lowercaseServiceStructName:     lowercaseServiceStructName,
		lowercaseClientStructName:      lowercaseClientStructName,
		serviceStructName:              serviceStructName,
		clientStructName:               clientStructName,
		serviceConnsName:               serviceConnsName,
		serviceConfigName:              serviceConfigName,
		serviceMutexName:               serviceMutexName,
		clientConnsName:                clientConnsName,
		clientMutexName:                clientMutexName,
		clientConfigName:               clientConfigName,
		clientMessagePoolName:          clientMessagePoolName,
		serviceUnderlyingInterfaceType: serviceUnderlyingInterfaceType,
		serviceUnderlyingInterfaceName: serviceUnderlyingInterfaceName,
	}

	// Add a package comment, so IDEs detect files as Generated
	f.PackageComment("Code Generated by generator, DO NOT EDIT.")

	imports := jen.Empty()
	imports.Add(jen.Lit("encoding/gob"), jen.Line())
	imports.Add(jen.Id("itf").Lit(itfImportPath), jen.Line())
	imports.Add(jen.Id("rdc").Lit(radRPCClient), jen.Line())
	imports.Add(jen.Id("rds").Lit(radRPCService), jen.Line())
	imports.Add(
		jen.Id(config.ImplementationPackageName).Lit(config.ImplementationPackage),
		jen.Line(),
	)
	f.Add(jen.Id("import").Parens(imports))

	// Generate the main struct
	mainStruct := GenerateMainStruct(
		generatorConfig,
	)

	// Generate the parseRPCMessage method
	parseRPCMethod := GenerateServiceMethods(
		generatorConfig,
	)

	// Generate the StartService and StopService method
	startStopServiceMethod := GenerateStartAndStopServiceMethod(
		generatorConfig,
	)

	// Generate the client methods
	generateClientMethods := GenerateClientMethods(
		generatorConfig,
	)

	// Generate client connect and disconnect method
	clientConAndDisconnMethod := GenerateClientConAndDisconnMethod(
		generatorConfig,
	)

	// Add the main struct
	f.Add(mainStruct)

	// Add the parseRPCMessage method
	f.Add(parseRPCMethod)

	// Add the StartService and StopService method
	f.Add(startStopServiceMethod)

	// Add the client methods
	f.Add(generateClientMethods)

	// Generate client connect method
	f.Add(clientConAndDisconnMethod)

	// Build the target file name
	targetDirectoryName := filepath.Join(
		filepath.Dir(config.ImplementationDirectory),
		config.ImplementationPackageName+"_rad_gen",
	)
	targetFilename := config.ImplementationPackageName + "_rad_gen.go"

	// Joined path
	if _, err := os.Stat(targetDirectoryName); os.IsNotExist(err) {
		if err = os.MkdirAll(targetDirectoryName, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}
	joinedPath := filepath.Join(targetDirectoryName, targetFilename)

	// Write Generated file
	if err := f.Save(joinedPath); err != nil {
		return fmt.Errorf("failed to save file: %w", err)
	}
	return nil
}
