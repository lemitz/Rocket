package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"rocket/internal/client"
	"rocket/internal/config"
	"rocket/internal/desktop"
	"rocket/internal/discovery"
	"rocket/internal/fsx"
	"rocket/internal/outbox"
	"rocket/internal/server"
)

func main() {
	gui := len(os.Args) == 1 || len(os.Args) > 1 && os.Args[1] == "gui"
	if gui {
		hideDedicatedConsole()
	}
	if err := run(os.Args[1:]); err != nil {
		if gui {
			showStartupError(err)
		} else {
			fmt.Fprintln(os.Stderr, "rocket:", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"gui"} // Double-clicking rocket.exe opens the native PC dashboard.
	}
	if args[0] == "help" {
		fmt.Println("usage: rocket [gui] | daemon; rocket settings show|set-download-dir PATH|reset-download-dir; rocket firewall install; rocket ping|put FILE [options]")
		return nil
	}
	if args[0] == "firewall" {
		if len(args) != 2 || args[1] != "install" {
			return errors.New("usage: rocket firewall install (run from an elevated terminal; private network and local subnet only)")
		}
		return installFirewall()
	}
	store, err := config.DefaultStore()
	if err != nil {
		return err
	}
	settings, err := store.Load()
	if err != nil {
		return err
	}
	if args[0] == "settings" {
		if len(args) < 2 {
			return errors.New("settings: expected show, set-download-dir or reset-download-dir")
		}
		switch args[1] {
		case "show":
			if len(args) != 2 {
				return errors.New("settings show: no arguments expected")
			}
			path, err := settings.ResolvedDownloadDir()
			if err != nil {
				return err
			}
			fmt.Println("download_dir:", path)
			fmt.Println("config:", store.Path)
			return nil
		case "set-download-dir":
			if len(args) != 3 {
				return errors.New("settings set-download-dir: expected one absolute directory path")
			}
			settings.DownloadDir = args[2]
			return store.Save(settings)
		case "reset-download-dir":
			if len(args) != 2 {
				return errors.New("settings reset-download-dir: no arguments expected")
			}
			settings.DownloadDir = ""
			return store.Save(settings)
		default:
			return errors.New("unknown settings command")
		}
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	port := fs.String("port", "42070", "TCP port")
	host := fs.String("host", "0.0.0.0", "daemon listen address (0.0.0.0 exposes files to your network)")
	dirFlag := fs.String("dir", "", "override download directory for this command")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] == "daemon" || args[0] == "gui" {
		settings, err = store.EnsureNodeID(settings) // Persist only saved settings, never a one-off --dir.
		if err != nil {
			return fmt.Errorf("prepare PC identity: %w", err)
		}
	}
	if *dirFlag != "" {
		settings.DownloadDir = *dirFlag
	}
	if args[0] == "daemon" || args[0] == "gui" {
		if _, err := settings.ResolvedDownloadDir(); err != nil {
			return err
		}
	}
	if args[0] == "daemon" || args[0] == "gui" {
		if fs.NArg() != 0 {
			return fmt.Errorf("%s: unexpected arguments", args[0])
		}
		dir, _ := settings.ResolvedDownloadDir()
		pcName, err := os.Hostname()
		if err != nil || pcName == "" {
			pcName = "Rocket PC"
		}
		ip := net.ParseIP(*host).To4()
		if ip == nil {
			return errors.New("daemon -host: expected an IPv4 address")
		}
		ln, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), *port))
		if err != nil {
			return err
		}
		defer ln.Close()
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: discovery.Port})
		udpOnline := err == nil
		if err != nil {
			fmt.Fprintln(os.Stderr, "Rocket: UDP discovery unavailable (manual IP still works):", err)
		} else {
			defer udp.Close()
			go discovery.Serve(udp, settings.NodeID, pcName)
		}
		outgoing := outbox.New()
		srv := server.Server{Dir: dir, Name: pcName, ID: settings.NodeID, Outbox: outgoing}
		if args[0] == "gui" {
			var currentDir atomic.Value
			currentDir.Store(dir)
			srv.DirProvider = func() string { return currentDir.Load().(string) }
			events := make(chan desktop.Event, 128)
			srv.OnEvent = func(e desktop.Event) {
				if e.Kind == "progress" {
					select {
					case events <- e:
					default: // Rendering may lag a fast transfer; never stall disk or network I/O.
					}
					return
				}
				events <- e
			}
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
					events <- desktop.Event{Kind: "error", Message: "Rocket server stopped: " + err.Error()}
				}
			}()
			var lastPhone atomic.Value
			lastPhone.Store(settings.LastPhone)
			opts := desktop.Options{
				Name: pcName, ID: settings.NodeID,
				Addresses: privateAddresses(*port),
				UDPOnline: udpOnline,
				LastPhone: func() string { return lastPhone.Load().(string) },
				RememberPhone: func(id string) {
					lastPhone.Store(id)
					// Reload from disk so a one-off --dir override is never persisted by accident.
					if saved, err := store.Load(); err == nil {
						saved.LastPhone = id
						_ = store.Save(saved)
					}
				},
				Unqueue:     outgoing.Drop,
				FirewallOK:  firewallRulesPresent,
				FixFirewall: installFirewallElevated,
				Phones: func() []desktop.Phone {
					phones := outgoing.Phones()
					result := make([]desktop.Phone, 0, len(phones))
					for _, p := range phones {
						result = append(result, desktop.Phone{ID: p.ID, Name: p.Name, LastSeen: p.LastSeen})
					}
					return result
				},
				Queue:  outgoing.Queue,
				Folder: func() string { return currentDir.Load().(string) },
				ChangeFolder: func(path string) error {
					clean, err := config.ValidateDirectory(path)
					if err != nil {
						return err
					}
					if err := fsx.CleanOldParts(clean, 24*time.Hour); err != nil {
						return err
					}
					updated := settings
					updated.DownloadDir = clean
					if err := store.Save(updated); err != nil {
						return err
					}
					currentDir.Store(clean) // New connections use this path; in-flight files finish in the old one.
					return nil
				},
			}
			return desktop.Run(events, opts)
		}
		fmt.Printf("Rocket PC: %s; downloads: %s; TCP: %s\n", pcName, dir, ln.Addr())
		showAddresses(*port)
		fmt.Println("If phones cannot discover or connect: run 'rocket.exe firewall install' once from an Administrator terminal (Private/LocalSubnet only).")
		fmt.Println("WARNING: no authentication or encryption; anyone with network access can read and upload files.")
		fmt.Println("Keep this window open while using Rocket. Close it to stop the PC daemon.")
		return srv.Serve(ln)
	}
	if args[0] != "ping" && args[0] != "put" {
		return errors.New("unknown command")
	}
	c, err := client.Dial(*port)
	if err != nil {
		return err
	}
	defer c.Close()
	switch args[0] {
	case "ping":
		if fs.NArg() != 0 {
			return errors.New("ping: unexpected arguments")
		}
		name, err := c.Ping()
		if err == nil {
			fmt.Println(name)
		}
		return err
	case "put":
		if fs.NArg() != 1 {
			return errors.New("put: expected a file path")
		}
		saved, err := c.Put(fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Println("saved:", saved)
	}
	return nil
}

var firewallRules = []struct{ name, protocol, port string }{
	{"Rocket TCP", "TCP", "42070"},
	{"Rocket UDP", "UDP", "42069"},
}

// netsh runs without flashing a console window; Rocket's own console is hidden in GUI mode.
func netsh(args ...string) ([]byte, error) {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd.CombinedOutput()
}

// firewallRulesPresent reports whether both inbound Rocket rules exist. Read-only.
func firewallRulesPresent() bool {
	for _, rule := range firewallRules {
		if _, err := netsh("advfirewall", "firewall", "show", "rule", "name="+rule.name); err != nil {
			return false
		}
	}
	return true
}

// installFirewallElevated re-runs `rocket.exe firewall install` through a UAC prompt. It only
// happens when the user presses "Fix" in settings; Rocket never changes the firewall on its own.
func installFirewallElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, file, params := syscall.StringToUTF16Ptr("runas"), syscall.StringToUTF16Ptr(exe), syscall.StringToUTF16Ptr("firewall install")
	r, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(params)), 0, 0) // SW_HIDE
	if r <= 32 {
		return fmt.Errorf("elevation was not granted: %v", callErr)
	}
	return nil
}

// Only an explicit command may change Windows Firewall rules. Never do this at startup.
func installFirewall() error {
	for _, rule := range firewallRules {
		// Replace rather than duplicate when the command is run again.
		_, _ = netsh("advfirewall", "firewall", "delete", "rule", "name="+rule.name)
		args := []string{"advfirewall", "firewall", "add", "rule",
			"name=" + rule.name, "dir=in", "action=allow", "profile=private",
			"remoteip=localsubnet", "protocol=" + rule.protocol, "localport=" + rule.port}
		out, err := exec.Command("netsh", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("install %s firewall rule: %w (%s). Run this command as Administrator", rule.name, err, out)
		}
		fmt.Printf("%s: %s\n", rule.name, out)
	}
	return nil
}

// 0.0.0.0 is a listen address, not a destination. Prefer actual LAN adapters
// over VM-only interfaces in the small desktop address list.
func privateAddresses(port string) []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var real, virtual []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
				continue
			}
			label := iface.Name + "  " + net.JoinHostPort(ip.String(), port)
			if name := strings.ToLower(iface.Name); strings.Contains(name, "vmware") || strings.Contains(name, "virtual") || strings.Contains(name, "vethernet") {
				virtual = append(virtual, label)
			} else {
				real = append(real, label)
			}
		}
	}
	return append(real, virtual...)
}

func showAddresses(port string) {
	for _, address := range privateAddresses(port) {
		fmt.Println("Phone IPv4 on", address)
	}
}

// When Explorer starts the console-subsystem exe, keep the lightweight native
// window without a second black console. Never hide a parent PowerShell window.
func hideDedicatedConsole() {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	var ids [16]uint32
	count, _, _ := kernel.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	if count != 1 {
		return
	}
	hwnd, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(hwnd, 0)
	}
}

func showStartupError(err error) {
	messageText := "Rocket could not start: " + err.Error() + "\n\nIf Rocket is already running, close its existing window first."
	if desktop.IsRussianUI() {
		messageText = "Не удалось запустить Rocket: " + err.Error() + "\n\nЕсли Rocket уже запущен, сначала закройте его окно."
	}
	message := syscall.StringToUTF16Ptr(messageText)
	title := syscall.StringToUTF16Ptr("Rocket")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0,
		uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
