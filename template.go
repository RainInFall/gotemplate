// Reads the templates and writes the substituted templates

package main

import (
	"bytes"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"regexp"
	"strings"
)

// Holds the desired template
type template struct {
	Package      string
	Name         string
	Args         []string
	NewPackage   string
	Dir          string
	templateName string
	templateArgs []string
	mappings     map[string]string
	newIsPublic  bool
	inputFile    string
	replaceFrom  []string
	replaceTo    []string
}

// findPackageName reads all the go packages in the curent directory
// and finds which package they are in
func findPackageName() string {
	p, err := build.Default.Import(".", ".", build.ImportMode(0))
	if err != nil {
		fatalf("Failed to read packages in current directory: %v", err)
	}
	return p.Name
}

// init the template instantiation
func newTemplate(dir, pkg, templateArgsString string) *template {
	name, templateArgs := parseTemplateAndArgs(templateArgsString)
	return &template{
		Package:     pkg,
		Name:        name,
		Args:        templateArgs,
		Dir:         dir,
		mappings:    make(map[string]string),
		NewPackage:  findPackageName(),
		replaceFrom: make([]string, 0),
		replaceTo:   make([]string, 0),
	}
}

// Add a mapping for identifier
func (t *template) addMapping(name string) {
	replacementName := name
	// Replace word int Method
	replaced := false
	for i := range t.replaceFrom {
		debugf("Replace: %s %s", replacementName, t.replaceFrom[i])
		if strings.Index(replacementName, t.replaceFrom[i]) != -1 {
			replaced = true
			replacementName = strings.Replace(replacementName, t.replaceFrom[i], t.replaceTo[i], -1)
		}
	}
	if replaced {
		t.mappings[name] = replacementName
		return
	}
	if !strings.Contains(name, t.templateName) {
		// If name doesn't contain template name then just prefix it
		innerName := strings.ToUpper(t.Name[:1]) + t.Name[1:]
		replacementName = name + innerName
		debugf("Top level definition '%s' doesn't contain template name '%s', using '%s'", name, t.templateName, replacementName)
	} else {
		// make sure the new identifier will follow
		// Go casing style (newMySet not newmySet).
		innerName := t.Name
		if strings.Index(name, t.templateName) != 0 {
			innerName = strings.ToUpper(innerName[:1]) + innerName[1:]
		}
		replacementName = strings.Replace(name, t.templateName, innerName, 1)
	}
	// If new template name is not public then make sure
	// the exported name is not public too
	if !t.newIsPublic && ast.IsExported(replacementName) {
		replacementName = strings.ToLower(replacementName[:1]) + replacementName[1:]
	}
	t.mappings[name] = replacementName
}

// Parse the arguments string Template(A, B, C)
func parseTemplateAndArgs(s string) (name string, args []string) {
	expr, err := parser.ParseExpr(s)
	if err != nil {
		fatalf("Failed to parse %q: %v", s, err)
	}
	debugf("expr = %#v\n", expr)
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		fatalf("Failed to parse %q: expecting Identifier(...)", s)
	}
	debugf("fun = %#v", callExpr.Fun)
	fn, ok := callExpr.Fun.(*ast.Ident)
	if !ok {
		fatalf("Failed to parse %q: expecting Identifier(...)", s)
	}
	name = fn.Name
	for i, arg := range callExpr.Args {
		var buf bytes.Buffer
		debugf("arg[%d] = %#v", i, arg)
		format.Node(&buf, token.NewFileSet(), arg)
		s := buf.String()
		debugf("parsed = %q", s)
		args = append(args, s)
	}
	return
}

// "template type Set(A)"
var matchTemplateType = regexp.MustCompile(`^//\s*template\s+type\s+(\w+\s*.*?)\s*$`)

func (t *template) findTemplateDefinition(f *ast.File) {
	// Inspect the comments
	t.templateName = ""
	t.templateArgs = nil
	for _, cg := range f.Comments {
		for _, x := range cg.List {
			matches := matchTemplateType.FindStringSubmatch(x.Text)
			if matches != nil {
				if t.templateName != "" {
					fatalf("Found multiple template definitions in %s", t.inputFile)
				}
				t.templateName, t.templateArgs = parseTemplateAndArgs(matches[1])
			}
		}
	}
	if t.templateName == "" {
		fatalf("Didn't find template definition in %s", t.inputFile)
	}
	if len(t.templateArgs) != len(t.Args) {
		fatalf("Wrong number of arguments - template is expecting %d but %d supplied", len(t.Args), len(t.templateArgs))
	}
	debugf("templateName = %v, templateArgs = %v", t.templateName, t.templateArgs)
}

// Ouput the go formatted file
//
// Exits with a fatal error on error
func outputFile(fset *token.FileSet, f *ast.File, path string) {
	fd, err := os.Create(path)
	if err != nil {
		fatalf("Failed to open %q: %s", path, err)
	}
	if err := format.Node(fd, fset, f); err != nil {
		fatalf("Failed to format %q: %s", path, err)
	}
	err = fd.Close()
	if err != nil {
		fatalf("Failed to close %q: %s", path, err)
	}
}

// Parses a file into a Fileset and Ast
//
// Dies with a fatal error on error
func parseFile(path string) (*token.FileSet, *ast.File) {
	fset := token.NewFileSet() // positions are relative to fset
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		fatalf("Failed to parse file: %s", err)
	}
	return fset, f
}

// Replace the identifers in f
func replaceIdentifier(f *ast.File, old, new string) {
	// Inspect the AST and print all identifiers and literals.
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			// We replace the identifier name
			// which is a bit untidy if we weren't
			// replacing with an identifier
			if x.Name == old {
				x.Name = new
			}
		}
		return true
	})
}

// Return true if name is a template argument
func (t *template) isTemplateArgument(name string) bool {
	for _, item := range t.templateArgs {
		if item == name {
			return true
		}
	}
	return false
}

// Parses the template file
func (t *template) parse(inputFile string) {
	t.inputFile = inputFile
	// Make the name mappings
	t.newIsPublic = ast.IsExported(t.Name)

	fset, f := parseFile(inputFile)
	t.findTemplateDefinition(f)

	// debugf("Decls = %#v", f.Decls)
	// Find names which need to be adjusted
	namesToMangle := []string{}
	newDecls := []ast.Decl{}
	for _, Decl := range f.Decls {
		remove := false
		switch d := Decl.(type) {
		case *ast.GenDecl:
			// A general definition
			switch d.Tok {
			case token.IMPORT:
				// Ignore imports
			case token.CONST, token.VAR:
				// Find and remove identifiers found in template
				// params
				emptySpecs := []int{}
				for i, spec := range d.Specs {
					namesToRemove := []int{}
					v := spec.(*ast.ValueSpec)
					for j, name := range v.Names {
						debugf("VAR or CONST %v", name.Name)
						namesToMangle = append(namesToMangle, name.Name)
						if t.isTemplateArgument(name.Name) {
							namesToRemove = append(namesToRemove, j)
						}
					}
					// Shuffle the names to remove out of v.Names and v.Values
					for i := len(namesToRemove) - 1; i >= 0; i-- {
						p := namesToRemove[i]
						v.Names = append(v.Names[:p], v.Names[p+1:]...)
						v.Values = append(v.Values[:p], v.Values[p+1:]...)
					}
					// If empty then add to slice to remove later
					if len(v.Names) == 0 {
						emptySpecs = append(emptySpecs, i)
					}
				}
				// Remove now-empty specs
				for i := len(emptySpecs) - 1; i >= 0; i-- {
					p := emptySpecs[i]
					d.Specs = append(d.Specs[:p], d.Specs[p+1:]...)
				}
				remove = len(d.Specs) == 0
			case token.TYPE:
				namesToRemove := []int{}
				for i, spec := range d.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					debugf("Type %v", typeSpec.Name.Name)
					namesToMangle = append(namesToMangle, typeSpec.Name.Name)
					// Remove type A if it is a template definition
					if t.isTemplateArgument(typeSpec.Name.Name) {
						namesToRemove = append(namesToRemove, i)
					}
				}
				for i := len(namesToRemove) - 1; i >= 0; i-- {
					p := namesToRemove[i]
					d.Specs = append(d.Specs[:p], d.Specs[p+1:]...)
				}
				remove = len(d.Specs) == 0
			default:
				logf("Unknown type %s", d.Tok)
			}
			debugf("GenDecl = %#v", d)
		case *ast.FuncDecl:
			// A function definition
			if d.Recv != nil {
				// Has receiver so is a method, doing replace
				for _, from := range t.replaceFrom {
					if strings.Contains(d.Name.Name, from) {
						namesToMangle = append(namesToMangle, d.Name.Name)
						break
					}
				}
			} else {
				//debugf("FuncDecl = %#v", d)
				debugf("FuncDecl = %s", d.Name.Name)
				namesToMangle = append(namesToMangle, d.Name.Name)
				// Remove func A() if it is a template definition
				remove = t.isTemplateArgument(d.Name.Name)
			}
		default:
			fatalf("Unknown Decl %#v", Decl)
		}
		if !remove {
			newDecls = append(newDecls, Decl)
		}
	}
	debugf("Names to mangle = %#v", namesToMangle)

	// Remove the stub type definitions "type A int" from the package
	f.Decls = newDecls

	// Map the type definitions A -> string, B -> int
	for i := range t.Args {
		t.mappings[t.templateArgs[i]] = t.Args[i]
	}

	found := false
	for _, name := range namesToMangle {
		if name == t.templateName {
			found = true
			t.addMapping(name)
		} else if _, found := t.mappings[name]; !found {
			t.addMapping(name)
		}

	}
	if !found {
		fatalf("No definition for template type '%s'", t.templateName)
	}
	debugf("mappings = %#v", t.mappings)

	// Replace the identifiers
	for name, replacement := range t.mappings {
		replaceIdentifier(f, name, replacement)
	}

	// Change the package to the local package name
	f.Name.Name = t.NewPackage

	// Output
	outputFileName := "gotemplate_" + t.Name + ".go"
	outputFile(fset, f, outputFileName)

	// gofmt one last time to sort out messy identifier substution
	fset, f = parseFile(outputFileName)
	outputFile(fset, f, outputFileName)
	logf("Written '%s'", outputFileName)
}

// Instantiate the template package
func (t *template) instantiate() {
	logf("Substituting %q with %s(%s) into package %s", t.Package, t.Name, strings.Join(t.Args, ","), t.NewPackage)

	p, err := build.Default.Import(t.Package, t.Dir, build.ImportMode(0))
	if err != nil {
		fatalf("Import %s failed: %s", t.Package, err)
	}
	//debugf("package = %#v", p)
	debugf("Dir = %#v", p.Dir)
	// FIXME CgoFiles ?
	debugf("Go files = %#v", p.GoFiles)

	if len(p.GoFiles) == 0 {
		fatalf("No go files found for package '%s'", t.Package)
	}
	// FIXME
	if len(p.GoFiles) != 1 {
		fatalf("Found more than one go file in '%s' - can only cope with 1 for the moment, sorry", t.Package)
	}

	templateFilePath := path.Join(p.Dir, p.GoFiles[0])
	t.parse(templateFilePath)
}

// SetSwap input the swap pair
func (t *template) setSwap(swap string) {
	debugf("Parsing swap parameters:%s", swap)

	pairStrings := strings.Split(swap, ",")
	for _, pairString := range pairStrings {
		pair := strings.Split(pairString, "=")
		if len(pair) != 2 {
			fatalf("Swap paramters wrong!")
		}
		t.replaceFrom = append(t.replaceFrom, "_"+strings.TrimSpace(pair[0])+"_")
		t.replaceTo = append(t.replaceTo, strings.TrimSpace(pair[1]))
	}
	debugf("from = %v", t.replaceFrom)
	debugf("to = %v", t.replaceTo)
}
