package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tiggy-manage-agent/internal/serverconfig"
)

const defaultSandboxImage = "coolfan1024/onlyboxes-runtime:default"

var (
	execLookPath     = exec.LookPath
	runDoctorCommand = func(command string, args ...string) error {
		cmd := exec.Command(command, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
		}
		return nil
	}
	filepathAbs = filepath.Abs
)

type sandboxDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type sandboxDoctorReport struct {
	Runtime string               `json:"runtime"`
	Root    string               `json:"root,omitempty"`
	Image   string               `json:"image,omitempty"`
	Docker  string               `json:"docker,omitempty"`
	Pull    bool                 `json:"pull"`
	Checks  []sandboxDoctorCheck `json:"checks"`
	OK      bool                 `json:"ok"`
}

func commandSandbox(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("sandbox command requires a subcommand")
	}
	switch args[0] {
	case "doctor":
		return commandSandboxDoctor(args[1:])
	default:
		return fmt.Errorf("unknown sandbox subcommand %q", args[0])
	}
}

func commandSandboxDoctor(args []string) error {
	if err := serverconfig.LoadDotEnv(".env"); err != nil {
		return err
	}

	flags := flag.NewFlagSet("sandbox doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	runtime := envOrDefaultCLI("TMA_TOOL_RUNTIME", "cloud_sandbox")
	root := os.Getenv("TMA_CLOUD_SANDBOX_ROOT")
	image := envOrDefaultCLI("TMA_CLOUD_SANDBOX_IMAGE", defaultSandboxImage)
	dockerCommand := "docker"
	pull := true
	flags.StringVar(&runtime, "runtime", runtime, "auto | cloud_sandbox | local_system")
	flags.StringVar(&root, "root", root, "workspace root path for cloud_sandbox runtime")
	flags.StringVar(&image, "image", image, "Onlyboxes image for cloud_sandbox runtime")
	flags.StringVar(&dockerCommand, "docker", dockerCommand, "docker command path or name")
	flags.BoolVar(&pull, "pull", pull, "pull missing sandbox image")
	if err := flags.Parse(args); err != nil {
		return err
	}

	report := runSandboxDoctor(runtime, root, image, dockerCommand, pull)
	if err := printJSON(report); err != nil {
		return err
	}
	if !report.OK {
		return fmt.Errorf("sandbox doctor found failed checks")
	}
	return nil
}

func runSandboxDoctor(runtime string, root string, image string, dockerCommand string, pull bool) sandboxDoctorReport {
	report := sandboxDoctorReport{
		Runtime: normalizeSandboxDoctorRuntime(runtime),
		Root:    root,
		Image:   strings.TrimSpace(image),
		Docker:  strings.TrimSpace(dockerCommand),
		Pull:    pull,
		OK:      true,
	}
	if report.Image == "" {
		report.Image = defaultSandboxImage
	}
	if report.Docker == "" {
		report.Docker = "docker"
	}
	report.addCheck("runtime", report.Runtime == "cloud_sandbox" || report.Runtime == "local_system", fmt.Sprintf("runtime %q", report.Runtime))
	if report.Runtime == "local_system" {
		report.addCheck("sandbox", true, "local_system does not use cloud_sandbox")
		return report
	}

	rootPath, rootErr := resolveDoctorRoot(root)
	if rootErr != nil {
		report.addCheck("workspace_root", false, rootErr.Error())
	} else {
		report.Root = rootPath
		report.addCheck("workspace_root", true, rootPath)
	}

	dockerPath, lookErr := execLookPath(report.Docker)
	if lookErr != nil {
		report.addCheck("docker_command", false, lookErr.Error())
		return report
	}
	report.addCheck("docker_command", true, dockerPath)

	if err := runDoctorCommand(report.Docker, "info"); err != nil {
		report.addCheck("docker_daemon", false, err.Error())
		return report
	}
	report.addCheck("docker_daemon", true, "docker daemon reachable")

	if err := runDoctorCommand(report.Docker, "image", "inspect", report.Image); err != nil {
		if !pull {
			report.addCheck("sandbox_image", false, fmt.Sprintf("image %q not found locally: %v", report.Image, err))
			return report
		}
		if pullErr := runDoctorCommand(report.Docker, "pull", report.Image); pullErr != nil {
			report.addCheck("sandbox_image", false, fmt.Sprintf("image %q not found locally and pull failed: %v", report.Image, pullErr))
			return report
		}
		report.addCheck("sandbox_image", true, fmt.Sprintf("pulled image %q", report.Image))
	} else {
		report.addCheck("sandbox_image", true, fmt.Sprintf("image %q present locally", report.Image))
	}
	return report
}

func (r *sandboxDoctorReport) addCheck(name string, ok bool, message string) {
	status := "ok"
	if !ok {
		status = "failed"
		r.OK = false
	}
	r.Checks = append(r.Checks, sandboxDoctorCheck{Name: name, Status: status, Message: message})
}

func normalizeSandboxDoctorRuntime(value string) string {
	runtime := strings.TrimSpace(strings.ToLower(value))
	if runtime == "" || runtime == "auto" {
		return "cloud_sandbox"
	}
	return runtime
}

func resolveDoctorRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		root = cwd
	}
	abs, err := filepathAbs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root is not a directory: %s", abs)
	}
	return abs, nil
}

func envOrDefaultCLI(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
