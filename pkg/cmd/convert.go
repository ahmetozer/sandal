package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func convert(args []string) error {

	thisFlags, args := SplitArgs(args)

	mksquashfsPath, _ := exec.LookPath("mksquashfs")

	// Parse the flags
	flags := flag.NewFlagSet("convert", flag.ExitOnError)
	var (
		help      bool
		comp      string
		block     string
		platform  string
		owerwrite bool
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&comp, "comp", "xz", "compression algorithm (lz4, zstd, xz, lzo, gzip, lzma)")
	flags.StringVar(&block, "block", "1M", "block size")
	flags.StringVar(&platform, "pf", defaultContainerPlatform(), "container platform (podman, docker)")
	flags.BoolVar(&owerwrite, "ow", false, "owerwrite existing sqfs")
	flags.StringVar(&mksquashfsPath, "mksquashfs", mksquashfsPath, "path to mksquashfs binnary")
	flags.Parse(thisFlags)

	if l := len(args); l < 1 {
		fmt.Printf("Usage: %s convert [OPTIONS] CONTAINER \n\nOPTIONS:\n", os.Args[0])
		flags.PrintDefaults()

		return nil
	}

	if help {
		flags.PrintDefaults()
		return nil
	}

	if mksquashfsPath == "" {
		return fmt.Errorf("mksquashfs not found, ensure squashfs-tools are installed")
	}

	i, err := inspectContainer(platform, args[0])
	if err != nil {
		return err
	}

	fileName := args[0] + ".sqfs"

	if _, err := os.Stat(fileName); err == nil {
		if !owerwrite {
			return fmt.Errorf("file %s already exists", fileName)
		}
		if err := os.Remove(fileName); err != nil {
			return err
		}
	}
	mksquashfsArgs := []string{i[0].GraphDriver.Data.MergedDir, fileName, "-comp", comp, "-b", block, "-quiet"}

	newArgs := append(mksquashfsArgs, args[1:]...)
	cmd := exec.Command(mksquashfsPath, newArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Start()
	sig, err := cmd.Process.Wait()
	exitCode = sig.ExitCode()
	return err
}

// type ps {}

func inspectContainer(contianerPlatform, containerId string) (containerInspect, error) {
	i := containerInspect{}
	if contianerPlatform == "" {
		return i, fmt.Errorf("container platform not found")
	}
	if containerId == "" {
		return i, fmt.Errorf("container id not found")
	}

	args := []string{"inspect", containerId}
	if contianerPlatform == "podman" {
		args = append(args, "--log-level", "error")
	}
	cmd := exec.Command(contianerPlatform, args...)
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("Error creating StdoutPipe:", err)
		return i, err
	}
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Start()
	sig, err := cmd.Process.Wait()
	if err != nil {
		return i, err
	}
	exitCode = sig.ExitCode()

	if exitCode == 0 {
		err = json.NewDecoder(outPipe).Decode(&i)
		return i, err
	}
	return i, fmt.Errorf("container inspect error")
}

func defaultContainerPlatform() string {
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman"
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	return ""
}

type containerInspect []struct {
	ID      string    `json:"Id"`
	Created time.Time `json:"Created"`
	Path    string    `json:"Path"`
	Args    []string  `json:"Args"`
	State   struct {
		OciVersion  string    `json:"OciVersion"`
		Status      string    `json:"Status"`
		Running     bool      `json:"Running"`
		Paused      bool      `json:"Paused"`
		Restarting  bool      `json:"Restarting"`
		OOMKilled   bool      `json:"OOMKilled"`
		Dead        bool      `json:"Dead"`
		Pid         int       `json:"Pid"`
		ConmonPid   int       `json:"ConmonPid"`
		ExitCode    int       `json:"ExitCode"`
		Error       string    `json:"Error"`
		StartedAt   time.Time `json:"StartedAt"`
		FinishedAt  time.Time `json:"FinishedAt"`
		Healthcheck struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           any    `json:"Log"`
		} `json:"Healthcheck"`
	} `json:"State"`
	Image           string   `json:"Image"`
	ImageName       string   `json:"ImageName"`
	Rootfs          string   `json:"Rootfs"`
	Pod             string   `json:"Pod"`
	ResolvConfPath  string   `json:"ResolvConfPath"`
	HostnamePath    string   `json:"HostnamePath"`
	HostsPath       string   `json:"HostsPath"`
	StaticDir       string   `json:"StaticDir"`
	OCIConfigPath   string   `json:"OCIConfigPath"`
	OCIRuntime      string   `json:"OCIRuntime"`
	ConmonPidFile   string   `json:"ConmonPidFile"`
	Name            string   `json:"Name"`
	RestartCount    int      `json:"RestartCount"`
	Driver          string   `json:"Driver"`
	MountLabel      string   `json:"MountLabel"`
	ProcessLabel    string   `json:"ProcessLabel"`
	AppArmorProfile string   `json:"AppArmorProfile"`
	EffectiveCaps   []string `json:"EffectiveCaps"`
	BoundingCaps    []string `json:"BoundingCaps"`
	ExecIDs         []any    `json:"ExecIDs"`
	GraphDriver     struct {
		Name string `json:"Name"`
		Data struct {
			LowerDir  string `json:"LowerDir"`
			MergedDir string `json:"MergedDir"`
			UpperDir  string `json:"UpperDir"`
			WorkDir   string `json:"WorkDir"`
		} `json:"Data"`
	} `json:"GraphDriver"`
}
