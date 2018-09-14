// Copyright 2018 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cmdError "github.com/operator-framework/operator-sdk/commands/operator-sdk/error"
	"github.com/operator-framework/operator-sdk/pkg/generator"
	"github.com/operator-framework/operator-sdk/pkg/scaffold"

	"github.com/spf13/cobra"
)

func NewNewCmd() *cobra.Command {
	newCmd := &cobra.Command{
		Use:   "new <project-name>",
		Short: "Creates a new operator application",
		Long: `The operator-sdk new command creates a new operator application and 
generates a default directory layout based on the input <project-name>. 

<project-name> is the project name of the new operator. (e.g app-operator)

For example:
	$ mkdir $GOPATH/src/github.com/example.com/
	$ cd $GOPATH/src/github.com/example.com/
	$ operator-sdk new app-operator
generates a skeletal app-operator application in $GOPATH/src/github.com/example.com/app-operator.
`,
		Run: newFunc,
	}

	newCmd.Flags().StringVar(&apiVersion, "api-version", "", "Kubernetes apiVersion and has a format of $GROUP_NAME/$VERSION (e.g app.example.com/v1alpha1)")
	newCmd.Flags().StringVar(&kind, "kind", "", "Kubernetes CustomResourceDefintion kind. (e.g AppService)")
	newCmd.Flags().StringVar(&operatorType, "type", "go", "Type of operator to initialize (e.g \"ansible\")")
	newCmd.Flags().BoolVar(&skipGit, "skip-git-init", false, "Do not init the directory as a git repository")
	newCmd.Flags().BoolVar(&generatePlaybook, "generate-playbook", false, "Generate a playbook skeleton. (Only used for --type ansible)")

	return newCmd
}

var (
	apiVersion       string
	kind             string
	operatorType     string
	projectName      string
	skipGit          bool
	generatePlaybook bool
)

const (
	gopath              = "GOPATH"
	src                 = "src"
	dep                 = "dep"
	ensureCmd           = "ensure"
	goOperatorType      = "go"
	ansibleOperatorType = "ansible"

	defaultDirFileMode = 0750
	defaultFileMode    = 0644
)

func newFunc(cmd *cobra.Command, args []string) {
	if len(args) != 1 {
		cmdError.ExitWithError(cmdError.ExitBadArgs, fmt.Errorf("new command needs 1 argument"))
	}
	parse(args)
	mustBeNewProject()
	verifyFlags()
	g := generator.NewGenerator(apiVersion, kind, operatorType, projectName, repoPath(), generatePlaybook)
	err := g.Render()
	if err != nil {
		cmdError.ExitWithError(cmdError.ExitError, fmt.Errorf("failed to create project %v: %v", projectName, err))
	}
	if operatorType == goOperatorType {
		doScaffold()
		pullDep()
	}
	initGit()
}

func parse(args []string) {
	if len(args) != 1 {
		log.Fatal("new command needs 1 argument")
	}
	projectName = args[0]
	if len(projectName) == 0 {
		log.Fatal("project-name must not be empty")
	}
}

// mustBeNewProject checks if the given project exists under the current diretory.
// it exits with error when the project exists.
func mustBeNewProject() {
	fp := filepath.Join(mustGetwd(), projectName)
	stat, err := os.Stat(fp)
	if err != nil && os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Fatalf("failed to determine if project (%v) exists", projectName)
	}
	if stat.IsDir() {
		log.Fatalf("project (%v) exists. please use a different project name or delete the existing one", projectName)
	}
}

func doScaffold() {
	// create cmd/manager dir
	fullProjectPath := filepath.Join(mustGetwd(), projectName)
	cmdDir := filepath.Join(fullProjectPath, "cmd", "manager")
	if err := os.MkdirAll(cmdDir, defaultDirFileMode); err != nil {
		log.Fatalf("failed to create %v: %v", cmdDir, err)
	}

	// generate cmd/manager/main.go
	cmdFilePath := filepath.Join(cmdDir, "main.go")
	cmdgen := scaffold.NewCmdCodegen(&scaffold.CmdInput{ProjectPath: repoPath()})
	buf := &bytes.Buffer{}
	err := cmdgen.Render(buf)
	if err != nil {
		log.Fatalf("failed to render the template for (%v): %v", cmdFilePath, err)
	}
	err = writeFileAndPrint(cmdFilePath, buf.Bytes(), defaultFileMode)
	if err != nil {
		log.Fatalf("failed to create %v: %v", cmdFilePath, err)
	}

	// create pkg/apis dir
	apisDir := filepath.Join(fullProjectPath, "pkg", "apis")
	if err := os.MkdirAll(apisDir, defaultDirFileMode); err != nil {
		log.Fatalf("failed to create %v: %v", cmdDir, err)
	}

	// generate pkg/apis/apis.go
	apisFilePath := filepath.Join(apisDir, "apis.go")
	apisgen := scaffold.NewAPIsCodegen()
	buf = &bytes.Buffer{}
	err = apisgen.Render(buf)
	if err != nil {
		log.Fatalf("failed to render the template for (%v): %v", apisFilePath, err)
	}
	err = writeFileAndPrint(apisFilePath, buf.Bytes(), defaultFileMode)
	if err != nil {
		log.Fatalf("failed to create %v: %v", apisFilePath, err)
	}

	// create pkg/controller dir
	controllerDir := filepath.Join(fullProjectPath, "pkg", "controller")
	if err := os.MkdirAll(controllerDir, defaultDirFileMode); err != nil {
		log.Fatalf("failed to create %v: %v", cmdDir, err)
	}

	// generate pkg/controller/controller.go
	controllerFilePath := filepath.Join(controllerDir, "controller.go")
	controllergen := scaffold.NewControllerCodegen()
	buf = &bytes.Buffer{}
	err = controllergen.Render(buf)
	if err != nil {
		log.Fatalf("failed to render the template for (%v): %v", controllerFilePath, err)
	}
	err = writeFileAndPrint(controllerFilePath, buf.Bytes(), defaultFileMode)
	if err != nil {
		log.Fatalf("failed to create %v: %v", controllerFilePath, err)
	}

	// TODO: generate rest of the scaffold.
}

// Writes file to a given path and data buffer, as well as prints out a message confirming creation of a file
func writeFileAndPrint(filePath string, data []byte, fileMode os.FileMode) error {
	if err := ioutil.WriteFile(filePath, data, fileMode); err != nil {
		return err
	}
	fmt.Printf("Create %v \n", filePath)
	return nil
}

// repoPath checks if this project's repository path is rooted under $GOPATH and returns project's repository path.
// repoPath field on generator is used primarily in generation of Go operator. For Ansible we will set it to cwd
func repoPath() string {
	// We only care about GOPATH constraint checks if we are a Go operator
	wd := mustGetwd()
	if operatorType == goOperatorType {
		gp := os.Getenv(gopath)
		if len(gp) == 0 {
			cmdError.ExitWithError(cmdError.ExitError, fmt.Errorf("$GOPATH env not set"))
		}
		// check if this project's repository path is rooted under $GOPATH
		if !strings.HasPrefix(wd, gp) {
			cmdError.ExitWithError(cmdError.ExitError, fmt.Errorf("project's repository path (%v) is not rooted under GOPATH (%v)", wd, gp))
		}
		// compute the repo path by stripping "$GOPATH/src/" from the path of the current directory.
		rp := filepath.Join(string(wd[len(filepath.Join(gp, src)):]), projectName)
		// strip any "/" prefix from the repo path.
		return strings.TrimPrefix(rp, string(filepath.Separator))
	}
	return wd
}

func verifyFlags() {
	if operatorType != goOperatorType && operatorType != ansibleOperatorType {
		cmdError.ExitWithError(cmdError.ExitBadArgs, errors.New("--type can only be `go` or `ansible`"))
	}
	if operatorType != ansibleOperatorType && generatePlaybook {
		cmdError.ExitWithError(cmdError.ExitBadArgs, errors.New("--generate-playbook can only be used with --type `ansible`"))
	}
	if operatorType != goOperatorType {
		if len(apiVersion) == 0 {
			cmdError.ExitWithError(cmdError.ExitBadArgs, errors.New("--api-version must not have empty value"))
		}
		if len(kind) == 0 {
			cmdError.ExitWithError(cmdError.ExitBadArgs, errors.New("--kind must not have empty value"))
		}
		kindFirstLetter := string(kind[0])
		if kindFirstLetter != strings.ToUpper(kindFirstLetter) {
			cmdError.ExitWithError(cmdError.ExitBadArgs, errors.New("--kind must start with an uppercase letter"))
		}
		if strings.Count(apiVersion, "/") != 1 {
			cmdError.ExitWithError(cmdError.ExitBadArgs, fmt.Errorf("api-version has wrong format (%v); format must be $GROUP_NAME/$VERSION (e.g app.example.com/v1alpha1)", apiVersion))
		}
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to determine the full path of the current directory: %v", err)
	}
	return wd
}

func execCmd(stdout *os.File, cmd string, args ...string) {
	dc := exec.Command(cmd, args...)
	dc.Dir = filepath.Join(mustGetwd(), projectName)
	dc.Stdout = stdout
	dc.Stderr = os.Stderr
	err := dc.Run()
	if err != nil {
		log.Fatalf("failed to exec %s %#v: %v", cmd, args, err)
	}
}

func pullDep() {
	_, err := exec.LookPath(dep)
	if err != nil {
		log.Fatalf("looking for dep in $PATH: %v", err)
	}
	fmt.Fprintln(os.Stdout, "Run dep ensure ...")
	execCmd(os.Stdout, dep, ensureCmd, "-v")
	fmt.Fprintln(os.Stdout, "Run dep ensure done")
}

func initGit() {
	if skipGit {
		return
	}
	fmt.Fprintln(os.Stdout, "Run git init ...")
	execCmd(os.Stdout, "git", "init")
	execCmd(os.Stdout, "git", "add", "--all")
	execCmd(os.Stdout, "git", "commit", "-q", "-m", "INITIAL COMMIT")
	fmt.Fprintln(os.Stdout, "Run git init done")
}
