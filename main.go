// crtctl — настройка CRT-мониторов в Linux: справка, расчёт режимов, EDID, канал DP, KDE.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

const version = "0.3.0"

// T — короткое имя для перевода сообщений.
var T = i18n.T

type command struct {
	name    string
	summary string // английский текст, переводится при выводе
	root    bool
	run     func(args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"setup", i18n.N("plug in the CRT and run this: detects everything and sets it up step by step"), true, cmdSetup},
		{"info", i18n.N("show what is connected: monitor, EDID, adapter, limits, current mode"), false, cmdInfo},
		{"doctor", i18n.N("find problems and suggest the exact command that fixes each one"), false, cmdDoctor},
		{"modes", i18n.N("highest safe refresh rate for every resolution on this monitor and adapter"), false, cmdModes},
		{"calc", i18n.N("compute mode timings (DMT/CVT/GTF) and check them against the limits"), false, cmdCalc},
		{"try", i18n.N("show any resolution and refresh rate right now, with automatic undo"), false, cmdTry},
		{"edid", i18n.N("EDID tools: read (from the monitor), show (decode), build (create your own)"), false, cmdEDID},
		{"apply", i18n.N("install an EDID: right now and permanently"), true, cmdApply},
		{"reprobe", i18n.N("make the driver re-read the EDID without replugging the cable"), true, cmdReprobe},
		{"remove", i18n.N("undo everything crtctl installed for a connector"), true, cmdRemove},
		{"link", i18n.N("DisplayPort link: show, force, remove (for picky DP→VGA adapters)"), true, cmdLink},
		{"kde", i18n.N("custom modes in KDE Plasma: list, add, best, remove, set"), false, cmdKDE},
		{"profiles", i18n.N("monitor profiles: list, details, create your own"), false, cmdProfiles},
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, T("crtctl %s — CRT monitor setup for Linux", version))
	fmt.Fprintln(w)
	fmt.Fprintln(w, T("Usage: crtctl [--lang LANG] <command> [arguments]"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, T("Commands:"))
	for _, c := range commands {
		s := T(c.summary)
		if c.root {
			s += " " + T("[sudo]")
		}
		fmt.Fprintf(w, "  %-9s %s\n", c.name, s)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, T("Typical workflow:"))
	for _, l := range [][2]string{
		{"crtctl setup", T("detect and set up the CRT step by step")},
		{"crtctl try DP-2 1000x500@75", T("look at any mode, with automatic undo")},
		{"crtctl doctor", T("what is wrong and how to fix it")},
		{"crtctl modes DP-2", T("which refresh rates are really possible")},
		{"crtctl edid build DP-2 --best", T("build an EDID with the best modes")},
		{"sudo crtctl apply DP-2 crtctl-DP-2.bin --test", T("try it until the next reboot")},
		{"sudo crtctl apply DP-2 crtctl-DP-2.bin", T("install it permanently")},
	} {
		fmt.Fprintf(w, "  %-46s %s\n", l[0], l[1])
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, T("Help for a command: crtctl help <command>  (or crtctl <command> -h)"))
	fmt.Fprintln(w, T("Language: --lang %s, or the CRTCTL_LANG / LANG environment variables", strings.Join(i18n.Supported(), "|")))
}

// extractLang убирает --lang из аргументов (где бы он ни стоял) и включает язык.
func extractLang(args []string) ([]string, error) {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--lang" || a == "-lang":
			if i+1 >= len(args) {
				return nil, errors.New("--lang needs a value: " + strings.Join(i18n.Supported(), ", "))
			}
			if err := i18n.Set(args[i+1]); err != nil {
				return nil, err
			}
			i++
		case strings.HasPrefix(a, "--lang=") || strings.HasPrefix(a, "-lang="):
			if err := i18n.Set(a[strings.Index(a, "=")+1:]); err != nil {
				return nil, err
			}
		default:
			out = append(out, a)
		}
	}
	return out, nil
}

func main() {
	args, err := extractLang(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "crtctl:", err)
		os.Exit(2)
	}
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	name, rest := args[0], args[1:]
	switch name {
	case "-h", "--help", "help":
		if name == "help" && len(rest) > 0 {
			name, rest = rest[0], []string{"-h"}
			break
		}
		usage(os.Stdout)
		return
	case "-v", "--version", "version":
		fmt.Println("crtctl", version)
		return
	}
	for _, c := range commands {
		if c.name == name {
			if err := c.run(rest); err != nil && !errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(os.Stderr, T("error:"), err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintln(os.Stderr, T("unknown command %q", name))
	fmt.Fprintln(os.Stderr)
	usage(os.Stderr)
	os.Exit(2)
}

// newFlags создаёт набор флагов с переведённой справкой. synopsis и about — английские строки.
func newFlags(name, synopsis, about string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintln(w, T("Usage: crtctl %s %s", name, T(synopsis)))
		if about != "" {
			fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(T(about)))
		}
		printFlags(w, fs)
	}
	return fs
}

// printFlags — замена fs.PrintDefaults с переводом описаний и слова «по умолчанию».
func printFlags(w io.Writer, fs *flag.FlagSet) {
	first := true
	fs.VisitAll(func(f *flag.Flag) {
		if first {
			fmt.Fprintln(w, "\n"+T("Options:"))
			first = false
		}
		arg := ""
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bf.IsBoolFlag() {
			arg = " " + T("value")
		}
		fmt.Fprintf(w, "  --%s%s\n      %s", f.Name, arg, T(f.Usage))
		if f.DefValue != "" && f.DefValue != "0" && f.DefValue != "false" {
			fmt.Fprintf(w, " (%s)", T("default: %s", f.DefValue))
		}
		fmt.Fprintln(w)
	})
}

// parse разбирает флаги в любом месте командной строки (flag из stdlib останавливается
// на первом позиционном аргументе) и возвращает позиционные аргументы.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(os.Stdout)
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // пусть fs.Parse сообщит об ошибке
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return pos, nil
}
