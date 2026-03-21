package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Completion(args []string) error {
	f := flag.NewFlagSet("completion", flag.ContinueOnError)
	var (
		help  bool
		shell string
	)
	f.BoolVar(&help, "help", false, "show this help message")
	f.StringVar(&shell, "shell", "", "shell type: bash or zsh (default: auto-detect from $SHELL)")

	if err := f.Parse(args); err != nil {
		return err
	}

	if help {
		fmt.Printf(`Generate shell completion scripts for sandal.

Usage:
  sandal completion -shell bash
  sandal completion -shell zsh

Installation:
  Bash:
    eval "$(sandal completion -shell bash)"
    # Or save permanently:
    sandal completion -shell bash > /etc/bash_completion.d/sandal

  Zsh:
    eval "$(sandal completion -shell zsh)"
    # Or save permanently:
    sandal completion -shell zsh > "${fpath[1]}/_sandal"
`)
		return nil
	}

	if shell == "" {
		shell = detectShell()
	}

	switch shell {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	default:
		return fmt.Errorf("unsupported shell: %s (supported: bash, zsh)", shell)
	}
	return nil
}

func detectShell() string {
	sh := os.Getenv("SHELL")
	base := filepath.Base(sh)
	switch base {
	case "zsh":
		return "zsh"
	default:
		return "bash"
	}
}

func bashCompletion() string {
	return `# bash completion for sandal
# source <(sandal completion -shell bash)

_sandal_container_names() {
    local names
    names=$(sandal ps -dry 2>/dev/null | tail -n +2 | awk '{print $1}')
    COMPREPLY=($(compgen -W "${names}" -- "${cur}"))
}

_sandal() {
    local cur prev words cword
    _init_completion || return

    local subcommands="run ps convert kill stop rerun rm inspect daemon cmd clear exec snapshot export attach vm completion help"

    # Check if we're past a -- separator (stop completing flags)
    local i
    for ((i=1; i < cword; i++)); do
        if [[ "${words[i]}" == "--" ]]; then
            return
        fi
    done

    # Determine the subcommand
    local subcmd=""
    for ((i=1; i < cword; i++)); do
        case "${words[i]}" in
            -*)
                continue
                ;;
            *)
                subcmd="${words[i]}"
                break
                ;;
        esac
    done

    # Complete subcommand name at position 1
    if [[ -z "${subcmd}" ]]; then
        COMPREPLY=($(compgen -W "${subcommands}" -- "${cur}"))
        return
    fi

    # Complete based on subcommand
    case "${subcmd}" in
        run)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -d -t -name -ro -rm -startup -env-all -env-pass -dir -net -tmp -csize -chdir-type -resolv -hosts -ns-mnt -ns-ipc -ns-cgroup -ns-pid -ns-net -ns-user -ns-uts -chdir -rdir -v -lw -devtmpfs -user -rcp -rci -cap-add -cap-drop -privileged -snapshot -memory -cpu -vm" -- "${cur}"))
            fi
            ;;
        ps)
            COMPREPLY=($(compgen -W "-help -dry -ns" -- "${cur}"))
            ;;
        convert)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -comp -block -pf -ow -mksquashfs" -- "${cur}"))
            fi
            ;;
        kill)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -all -signal -timeout" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        stop)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -all -signal -timeout" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        rerun)
            _sandal_container_names
            ;;
        rm)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -all" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        inspect)
            _sandal_container_names
            ;;
        daemon)
            COMPREPLY=($(compgen -W "-help -install -read-interval" -- "${cur}"))
            ;;
        cmd)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-all" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        clear)
            COMPREPLY=($(compgen -W "-help -all" -- "${cur}"))
            ;;
        exec)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -env-all -env-pass -dir -user -ns-mnt -ns-ipc -ns-cgroup -ns-pid -ns-net -ns-user -ns-uts" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        snapshot)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -f -i -e" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        export)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help -from -image -targz -o -i -e" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        attach)
            if [[ "${cur}" == -* ]]; then
                COMPREPLY=($(compgen -W "-help" -- "${cur}"))
            else
                _sandal_container_names
            fi
            ;;
        vm)
            # Handle vm subcommands
            local vm_subcmd=""
            for ((i=2; i < cword; i++)); do
                case "${words[i]}" in
                    -*)
                        continue
                        ;;
                    *)
                        vm_subcmd="${words[i]}"
                        break
                        ;;
                esac
            done

            if [[ -z "${vm_subcmd}" ]]; then
                COMPREPLY=($(compgen -W "create run start list delete create-disk stop kill" -- "${cur}"))
                return
            fi

            case "${vm_subcmd}" in
                create)
                    COMPREPLY=($(compgen -W "-name -kernel -initrd -cmdline -disk -iso -mount -env -cpus -memory" -- "${cur}"))
                    ;;
                run)
                    COMPREPLY=($(compgen -W "-name -kernel -initrd -cmdline -disk -iso -mount -env -cpus -memory" -- "${cur}"))
                    ;;
                start)
                    COMPREPLY=($(compgen -W "-name" -- "${cur}"))
                    ;;
                list)
                    ;;
                delete)
                    COMPREPLY=($(compgen -W "-all" -- "${cur}"))
                    ;;
                create-disk)
                    COMPREPLY=($(compgen -W "-path -size" -- "${cur}"))
                    ;;
                stop)
                    COMPREPLY=($(compgen -W "-name" -- "${cur}"))
                    ;;
                kill)
                    COMPREPLY=($(compgen -W "-name -all -force" -- "${cur}"))
                    ;;
            esac
            ;;
        completion)
            COMPREPLY=($(compgen -W "-help -shell" -- "${cur}"))
            ;;
    esac
}

complete -F _sandal sandal
`
}

func zshCompletion() string {
	// Build the subcommand descriptions for zsh
	subcommands := []struct{ name, desc string }{
		{"run", "Run a container"},
		{"ps", "List containers"},
		{"convert", "Convert a container image to squashfs"},
		{"kill", "Kill a container"},
		{"stop", "Stop a container"},
		{"rerun", "Restart a container"},
		{"rm", "Remove a container"},
		{"inspect", "Get configuration file"},
		{"daemon", "Start sandal daemon"},
		{"cmd", "Get execution command"},
		{"clear", "Clear unused containers"},
		{"exec", "Execute a command in a container"},
		{"snapshot", "Snapshot container changes as a squashfs image"},
		{"export", "Export full container filesystem as a squashfs image"},
		{"attach", "Attach to a running background container"},
		{"vm", "Manage virtual machines (macOS only)"},
		{"completion", "Generate shell completion scripts"},
		{"help", "Show help"},
	}

	var subcmdList []string
	for _, sc := range subcommands {
		subcmdList = append(subcmdList, fmt.Sprintf("'%s:%s'", sc.name, sc.desc))
	}

	return `#compdef sandal
# zsh completion for sandal
# source <(sandal completion -shell zsh)

_sandal_container_names() {
    local -a containers
    containers=(${(f)"$(sandal ps -dry 2>/dev/null | tail -n +2 | awk '{print $1}')"})
    _describe 'container' containers
}

_sandal() {
    local -a subcommands
    subcommands=(
        ` + strings.Join(subcmdList, "\n        ") + `
    )

    _arguments -C \
        '1:subcommand:->subcmd' \
        '*::arg:->args'

    case $state in
        subcmd)
            _describe 'subcommand' subcommands
            ;;
        args)
            case ${words[1]} in
                run)
                    _arguments \
                        '-help[show this help message]' \
                        '-d[run container in background]' \
                        '-t[allocate a pseudo-TTY]' \
                        '-name[name of the container]:name:' \
                        '-ro[read only rootfs]' \
                        '-rm[remove container files on exit]' \
                        '-startup[run container at startup by sandal daemon]' \
                        '-env-all[send all environment variables to container]' \
                        '*-env-pass[pass specific environment variables]:var:' \
                        '-dir[working directory]:dir:_directories' \
                        '*-net[configure network interfaces]:config:' \
                        '-tmp[allocate changes at memory (MB)]:size:' \
                        '-csize[change dir disk image size]:size:' \
                        '-chdir-type[change dir type]:type:(auto folder image)' \
                        '-resolv[resolver config]:config:' \
                        '-hosts[hosts file handling]:config:(cp cp-n image)' \
                        '-ns-mnt[mnt namespace or host]:ns:' \
                        '-ns-ipc[ipc namespace or host]:ns:' \
                        '-ns-cgroup[cgroup namespace or host]:ns:' \
                        '-ns-pid[pid namespace or host]:ns:' \
                        '-ns-net[net namespace or host]:ns:' \
                        '-ns-user[user namespace or host]:ns:' \
                        '-ns-uts[uts namespace or host]:ns:' \
                        '-chdir[container changes directory]:dir:_directories' \
                        '-rdir[root filesystem directory]:dir:_directories' \
                        '*-v[volume mount point]:mount:' \
                        '*-lw[lower directory]:dir:_directories' \
                        '-devtmpfs[mount point of devtmpfs]:path:' \
                        '-user[user or user\:group]:user:' \
                        '*-rcp[run command before pivoting]:command:' \
                        '*-rci[run command before init]:command:' \
                        '*-cap-add[add capabilities]:cap:' \
                        '*-cap-drop[drop capabilities]:cap:' \
                        '-privileged[give extended privileges]' \
                        '-snapshot[snapshot output path]:path:_files' \
                        '-memory[memory limit (e.g., 512M, 1G)]:limit:' \
                        '-cpu[number of CPUs]:cpus:' \
                        '-vm[VM name (macOS only)]:vm:'
                    ;;
                ps)
                    _arguments \
                        '-help[show this help message]' \
                        '-dry[do not verify running state]' \
                        '-ns[show namespaces]'
                    ;;
                convert)
                    _arguments \
                        '-help[show this help message]' \
                        '-comp[compression algorithm]:algo:(lz4 zstd xz lzo gzip lzma)' \
                        '-block[block size]:size:' \
                        '-pf[container platform]:platform:(podman docker)' \
                        '-ow[overwrite existing sqfs]' \
                        '-mksquashfs[path to mksquashfs]:path:_files' \
                        '*:container:'
                    ;;
                kill)
                    _arguments \
                        '-help[show this help message]' \
                        '-all[kill all running containers]' \
                        '-signal[kill signal]:signal:' \
                        '-timeout[timeout to wait]:seconds:' \
                        '*:container:_sandal_container_names'
                    ;;
                stop)
                    _arguments \
                        '-help[show this help message]' \
                        '-all[stop all running containers]' \
                        '-signal[term signal]:signal:' \
                        '-timeout[timeout to wait]:seconds:' \
                        '*:container:_sandal_container_names'
                    ;;
                rerun)
                    _arguments \
                        '*:container:_sandal_container_names'
                    ;;
                rm)
                    _arguments \
                        '-help[show this help message]' \
                        '-all[remove all stopped containers]' \
                        '*:container:_sandal_container_names'
                    ;;
                inspect)
                    _arguments \
                        '*:container:_sandal_container_names'
                    ;;
                daemon)
                    _arguments \
                        '-help[show this help message]' \
                        '-install[install service files]' \
                        '-read-interval[disk reload interval]:duration:'
                    ;;
                cmd)
                    _arguments \
                        '-all[print all]' \
                        '*:container:_sandal_container_names'
                    ;;
                clear)
                    _arguments \
                        '-help[show this help message]' \
                        '-all[delete all non-running containers]'
                    ;;
                exec)
                    _arguments \
                        '-help[show this help message]' \
                        '-env-all[send all environment variables]' \
                        '*-env-pass[pass specific environment variables]:var:' \
                        '-dir[working directory]:dir:_directories' \
                        '-user[work user]:user:' \
                        '-ns-mnt[mnt namespace or host]:ns:' \
                        '-ns-ipc[ipc namespace or host]:ns:' \
                        '-ns-cgroup[cgroup namespace or host]:ns:' \
                        '-ns-pid[pid namespace or host]:ns:' \
                        '-ns-net[net namespace or host]:ns:' \
                        '-ns-user[user namespace or host]:ns:' \
                        '-ns-uts[uts namespace or host]:ns:' \
                        '*:container:_sandal_container_names'
                    ;;
                snapshot)
                    _arguments \
                        '-help[show this help message]' \
                        '-f[custom output file path]:path:_files' \
                        '*-i[include path]:path:_files' \
                        '*-e[exclude path]:path:_files' \
                        '*:container:_sandal_container_names'
                    ;;
                export)
                    _arguments \
                        '-help[show this help message]' \
                        '-from[create squashfs from directory]:dir:_directories' \
                        '-image[export image from registry]:image:' \
                        '-targz[export as tar.gz]' \
                        '-o[output file path]:path:_files' \
                        '*-i[include path]:path:_files' \
                        '*-e[exclude path]:path:_files' \
                        '*:container:_sandal_container_names'
                    ;;
                attach)
                    _arguments \
                        '-help[show this help message]' \
                        '*:container:_sandal_container_names'
                    ;;
                vm)
                    local -a vm_subcommands
                    vm_subcommands=(
                        'create:Create a new VM configuration'
                        'run:Run an ephemeral VM'
                        'start:Start a VM'
                        'list:List all VMs'
                        'delete:Delete VMs'
                        'create-disk:Create a raw disk image'
                        'stop:Gracefully stop a running VM'
                        'kill:Force kill a running VM'
                    )
                    _arguments -C \
                        '1:vm subcommand:->vm_subcmd' \
                        '*::vm_arg:->vm_args'
                    case $state in
                        vm_subcmd)
                            _describe 'vm subcommand' vm_subcommands
                            ;;
                        vm_args)
                            case ${words[1]} in
                                create)
                                    _arguments \
                                        '-name[VM name]:name:' \
                                        '-kernel[path to kernel]:path:_files' \
                                        '-initrd[path to initrd]:path:_files' \
                                        '-cmdline[kernel command line]:cmdline:' \
                                        '-disk[path to disk image]:path:_files' \
                                        '-iso[path to ISO image]:path:_files' \
                                        '*-mount[mount host dir]:mount:' \
                                        '*-env[environment variable]:env:' \
                                        '-cpus[number of CPUs]:cpus:' \
                                        '-memory[memory in MB]:memory:'
                                    ;;
                                run)
                                    _arguments \
                                        '-name[VM name]:name:' \
                                        '-kernel[path to kernel]:path:_files' \
                                        '-initrd[path to initrd]:path:_files' \
                                        '-cmdline[kernel command line]:cmdline:' \
                                        '-disk[path to disk image]:path:_files' \
                                        '-iso[path to ISO image]:path:_files' \
                                        '*-mount[mount host dir]:mount:' \
                                        '*-env[environment variable]:env:' \
                                        '-cpus[number of CPUs]:cpus:' \
                                        '-memory[memory in MB]:memory:'
                                    ;;
                                start)
                                    _arguments '-name[VM name]:name:'
                                    ;;
                                delete)
                                    _arguments '-all[delete all VMs]' '*:name:'
                                    ;;
                                create-disk)
                                    _arguments \
                                        '-path[output disk image path]:path:_files' \
                                        '-size[disk size in MB]:size:'
                                    ;;
                                stop)
                                    _arguments '-name[VM name]:name:'
                                    ;;
                                kill)
                                    _arguments \
                                        '-name[VM name]:name:' \
                                        '-all[kill all running VMs]' \
                                        '-force[skip SIGTERM, send SIGKILL]'
                                    ;;
                            esac
                            ;;
                    esac
                    ;;
                completion)
                    _arguments \
                        '-help[show this help message]' \
                        '-shell[shell type]:shell:(bash zsh)'
                    ;;
            esac
            ;;
    esac
}

compdef _sandal sandal
`
}
