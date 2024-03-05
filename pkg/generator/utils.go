package generator

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// FindAbsolutePathFromRelative finds the absolute path from a relative path.
func FindAbsolutePathFromRelative(relativePath string) string {
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
		FailErr(err)
	}
	return wd + "/" + relativePath
}

// FailErr prints the error and exits the program.
func FailErr(err error) {
	if err != nil {
		if _, fmtErr := fmt.Fprintf(os.Stderr, "Error: %v\n", err); fmtErr != nil {
			panic(fmtErr)
		}
		os.Exit(1)
	}
}

// isNamedType reports whether t is the named type path.name.
func IsNamedType(t types.Type, path, name string) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Name() == name && obj.Pkg() != nil && obj.Pkg().Path() == path
}

func GetPathAndQualifiedName(sourceType string, config Config) (string, string) {
	idx := strings.LastIndexByte(sourceType, '/')
	if idx == -1 {
		FailErr(
			fmt.Errorf(
				`expected qualified type as "pkg/path.MyType", however found: %v`,
				sourceType,
			),
		)
	}

	// Find the index of the first letter
	firstLetterIndex := FindFirstLetterIndex(sourceType)

	// Extract the package path and type name
	typeName := sourceType[:firstLetterIndex] + sourceType[idx+1:]
	packagePath := sourceType[firstLetterIndex:]

	if config.gobTypes == nil {
		config.gobTypes = make(map[string]string)
	}

	config.gobTypes[typeName] = packagePath
	return packagePath, typeName
}

func SplitSourceType(sourceType string) (string, string) {
	idx := strings.LastIndexByte(sourceType, '.')
	if idx == -1 {
		FailErr(
			fmt.Errorf(
				`expected qualified type as "pkg/path.MyType", however found: %v`,
				sourceType,
			),
		)
	}
	sourceTypePackage := sourceType[0:idx]
	sourceTypeName := sourceType[idx+1:]

	return sourceTypePackage, sourceTypeName
}

// LoadPackage loads the package and returns the package.
func LoadPackage(path string) *packages.Package {
	cfg := &packages.Config{Mode: packages.NeedTypes | packages.NeedImports}
	pkgs, err := packages.Load(cfg, path)
	if err != nil {
		FailErr(fmt.Errorf("loading packages for inspection: %w", err))
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	return pkgs[0]
}

// LoadRepository loads the repository and returns the packages.
func LoadRepository() ([]*packages.Package, error) {
	var pkgs []*packages.Package
	dir, err := os.Getwd()
	if err != nil {
		return pkgs, fmt.Errorf("getting working directory: %w", err)
	}

	// Traverse up until go.mod is found
	for {
		joinedPath := filepath.Join(dir, "go.mod")
		_, err = os.Stat(joinedPath)
		if err != nil {
			if os.IsNotExist(err) {
				// No go.mod file found
				dir = filepath.Dir(dir)
				if dir == "/" {
					return pkgs, fmt.Errorf("go.mod file not found in %s", dir)
				}
				continue
			}
		}
		break
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Dir:  dir,
	}

	pkgs, err = packages.Load(cfg, "./...")
	if err != nil {
		return pkgs, fmt.Errorf("loading packages for inspection: %w", err)
	}

	return pkgs, nil
}

// RequiresQual determines if the source type requires a qualified name.
func RequiresQual(sourceType string) bool {
	return strings.Contains(sourceType, ".")
}

// FindFirstLetterIndex is a helper function to find the index of the first letter in a string.
func FindFirstLetterIndex(input string) int {
	for i, char := range input {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') {
			return i
		}
	}
	return len(input)
}

// DefaultValueAsString generates the default value for a given types.Type and returns it as a string.
func DefaultValueAsString(t types.Type, config Config) (string, error) {
	// Check if the type is a pointer, and dereference it if it is
	if pointerType, ok := t.(*types.Pointer); ok {
		t = pointerType.Elem()
	}

	// Create a new zero value of the type
	var defaultValue any
	switch t := t.(type) {
	case *types.Basic:
		// Basic types (e.g., int, string, bool)
		switch t.Kind() {
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
			defaultValue = int64(0)
		case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			defaultValue = uint64(0)
		case types.Float32, types.Float64:
			defaultValue = float64(0)
		case types.Complex64, types.Complex128:
			defaultValue = complex128(0)
		case types.String:
			defaultValue = ""
		case types.Bool:
			defaultValue = false
		case types.Invalid, types.Uintptr, types.UnsafePointer,
			types.UntypedBool, types.UntypedInt, types.UntypedRune,
			types.UntypedFloat, types.UntypedComplex, types.UntypedString,
			types.UntypedNil:
			return "", fmt.Errorf("unsupported basic type: %v", t)
		}
	case *types.Named:
		// Named types
		_, qualifiedName := GetPathAndQualifiedName(t.String(), config)
		return qualifiedName + "{}", nil // Return typeName{}
	case *types.Slice, *types.Map, *types.Array:
		// Slice, map, and array types
		defaultValue = "nil"
	case *types.Struct:
		// Struct type
		structName := t.String()                         // Get the struct name
		structName = strings.TrimPrefix(structName, "*") // Remove any leading *
		return structName + "{}", nil                    // Return structName{}
	case *types.Chan, *types.Interface, *types.Signature:
		// Unsupported composite types
		return "", fmt.Errorf("unsupported composite type: %T -> %v", t, t)
	default:
		// Unsupported type
		return "", fmt.Errorf("unsupported type: %T -> %v", t, t)
	}

	// Convert the zero value to a string
	return fmt.Sprintf("%v", defaultValue), nil
}

// FlattenType recursively expands composite types until primitive types are reached.
func FlattenType(typ types.Type) types.Type {
	switch t := typ.(type) {
	case *types.Named:
		// If the type is a named type, expand its underlying type
		return FlattenType(t.Underlying())
	case *types.Array:
		// If the type is an array, expand its element type
		return types.NewSlice(FlattenType(t.Elem()))
	case *types.Slice:
		// If the type is a slice, expand its element type
		return types.NewSlice(FlattenType(t.Elem()))
	case *types.Map:
		// If the type is a map, expand its key and value types
		return types.NewMap(FlattenType(t.Key()), FlattenType(t.Elem()))
	case *types.Pointer:
		// If the type is a pointer, expand its element type
		return FlattenType(t.Elem())
	case *types.Chan:
		// If the type is a channel, expand its element type
		return FlattenType(t.Elem())
	default:
		// Return primitive types as is
		return t
	}
}

// GetPackageInfo returns the full import path and package name of a given type.
func GetPackageInfo(typ types.Type) (string, string) {
	// Extract the object associated with the type
	obj := typ.(*types.Named).Obj()

	// Get the package of the object
	pkg := obj.Pkg()

	// Obtain the full import path of the package
	fullPath := pkg.Path()

	// Obtain just the package name
	packageName := pkg.Name()

	return fullPath, packageName
}

// IsGobSerializable checks if all methods of the given interface type
// only contain data that's serializable by encoding/gob.
func IsGobSerializable(interfaceType *types.Interface) bool {
	// Iterate over all methods of the interface
	for i := 0; i < interfaceType.NumMethods(); i++ {
		method := interfaceType.Method(i)

		// Check parameters of the method
		for j := 0; j < method.Type().(*types.Signature).Params().Len(); j++ {
			paramType := method.Type().(*types.Signature).Params().At(j).Type()

			// Check if the parameter type is serializable by gob
			if !IsGobType(paramType) {
				return false
			}
		}

		// Check return types of the method
		for j := 0; j < method.Type().(*types.Signature).Results().Len(); j++ {
			resultType := method.Type().(*types.Signature).Results().At(j).Type()

			// Check if the return type is serializable by gob
			if !IsGobType(resultType) {
				return false
			}
		}
	}

	// All methods passed the serialization check
	return true
}

// IsGobType checks if the given type is serializable by encoding/gob.
func IsGobType(typ types.Type) bool {
	// Check if the type is one of the basic types supported by gob
	switch typ.Underlying().(type) {
	case *types.Basic:
		// Check if the basic type is one of the types supported by gob
		switch typ.Underlying().(*types.Basic).Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
			types.Uintptr, types.Float32, types.Float64, types.Complex64, types.Complex128,
			types.String:
			return true
		case types.Invalid, types.UnsafePointer, types.UntypedBool,
			types.UntypedInt, types.UntypedRune, types.UntypedFloat,
			types.UntypedComplex, types.UntypedString, types.UntypedNil:
			return false
		}
	case *types.Slice, *types.Array:
		// Slices and arrays are serializable if their element type is serializable
		elemType := typ.Underlying().(*types.Slice).Elem()
		return IsGobType(elemType)
	case *types.Pointer, *types.Map, *types.Chan, *types.Struct, *types.Interface:
		// Pointers, maps, channels, structs, and interfaces are not directly supported by gob
		// You can recursively check their underlying types to determine if they are serializable
		return IsGobType(typ.Underlying())
	default:
		// Unsupported types
		return false
	}
	return false
}

func ListImplementations(
	interfacePkgs []*packages.Package,
	interfaceName string,
) ([]types.Type, error) {
	interfaceSearched, findErr := findInterfaceInfo(interfaceName, interfacePkgs)
	if findErr != nil {
		return nil, fmt.Errorf("finding interface info: %w", findErr)
	}

	implementations := findImplementations(interfaceSearched, interfacePkgs)
	return implementations, nil
}

func findInterfaceInfo(
	interfaceName string,
	srcPackages []*packages.Package,
) (*types.Interface, error) {
	for _, pkg := range srcPackages {
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			if name == interfaceName {
				if typesObj, ok := scope.Lookup(name).(*types.TypeName); ok {
					return typesObj.Type().Underlying().(*types.Interface), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("interface %s not found", interfaceName)
}

func findImplementations(
	interfaceSearched *types.Interface,
	srcPackages []*packages.Package,
) []types.Type {
	var result []types.Type
	for _, pkg := range srcPackages {
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			if typesObj, okLookup := scope.Lookup(name).(*types.TypeName); okLookup {
				if _, okUnderlying := typesObj.Type().Underlying().(*types.Struct); okUnderlying {
					if types.Implements(typesObj.Type(), interfaceSearched) ||
						types.Implements(types.NewPointer(typesObj.Type()), interfaceSearched) {
						result = append(result, typesObj.Type())
					}
				}
			}
		}
	}
	return result
}
