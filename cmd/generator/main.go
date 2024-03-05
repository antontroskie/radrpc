//nolint:forbidigo // This is a command line tool
package main

import (
	"fmt"
	"go/types"
	"os"
	"strings"

	"github.com/antontroskie/radrpc/pkg/generator"
)

const (
	NoOfArguments = 2
)

func main() {
	// Handle arguments to command
	if len(os.Args) != NoOfArguments {
		generator.FailErr(fmt.Errorf("expected exactly one argument: <source type>"))
	}

	sourceType := os.Args[1]

	// Split source type into relative path and name
	implementationRelativePath, implementationName := generator.SplitSourceType(sourceType)

	// Inspect package and use type checker to infer imported types
	targetPkg := generator.LoadPackage(implementationRelativePath)

	// Lookup the given source type name in the package declarations
	obj := targetPkg.Types.Scope().Lookup(implementationName)
	if obj == nil {
		generator.FailErr(fmt.Errorf("%s not found in declared types of %s",
			implementationName, targetPkg))
	}

	// We check if it is a named type
	if _, ok := obj.(*types.TypeName); !ok {
		generator.FailErr(fmt.Errorf("%v is not a named type", obj))
	}

	// Load repository
	globalPkgs, err := generator.LoadRepository()
	if err != nil {
		generator.FailErr(err)
	}

	// We expect an interface type
	interfaceType, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		generator.FailErr(fmt.Errorf("%v is not an interface type", obj))
	}

	// List implementations
	implementations, err := generator.ListImplementations(globalPkgs, implementationName)
	if err != nil {
		generator.FailErr(err)
	}
	if len(implementations) != 1 {
		generator.FailErr(fmt.Errorf("expected exactly one implementation of %s, found %d",
			implementationName, len(implementations)))
	}

	// Get the first implementation
	implementation := implementations[0]

	// Get implementation path
	implementationPackage, _ := generator.SplitSourceType(implementation.String())

	// Find last '/' and split
	splittedImplementationPackage := strings.Split(implementationPackage, "/")
	implementationPackageName := splittedImplementationPackage[len(splittedImplementationPackage)-1]

	// Struct name
	serviceStructName := implementationName + "RDS"
	clientStructName := implementationName + "RDC"

	// Assign values to config
	config := generator.InitConfig{
		ServiceStructName:          serviceStructName,
		ClientStructName:           clientStructName,
		InterfaceType:              interfaceType,
		Implementation:             implementation,
		ImplementationPackageName:  implementationPackageName,
		ImplementationName:         implementationName,
		ImplementationRelativePath: implementationRelativePath,
		ImplementationPackage:      implementationPackage,
		ImplementationDirectory:    findAbsolutePathFromRelative(implementationRelativePath),
	}

	println("Generating for", config.ImplementationName)
	println("Implementation Package:", config.ImplementationPackage)
	println("Implementation Package Name:", config.ImplementationPackageName)
	println("Implementation Name:", config.ImplementationName)
	println("Implementation Relative Path:", config.ImplementationRelativePath)
	println("Implementation directory:", config.ImplementationDirectory)

	// Generate for interface type
	if err = generator.GenerateForInterface(config); err != nil {
		generator.FailErr(err)
	}
}

func findAbsolutePathFromRelative(relativePath string) string {
	// Determine if the path is relative or absolute
	if relativePath[0] == '/' {
		return relativePath
	}

	// Remove the dot
	if relativePath[0] == '.' && relativePath[1] == '/' {
		relativePath = relativePath[2:]
	}

	wd, err := os.Getwd()
	if err != nil {
		generator.FailErr(err)
	}
	return wd + "/" + relativePath
}
