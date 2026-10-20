// boottest — стенд для проверки постоянной установки в контейнерах настоящих дистрибутивов:
// вызывает тот же код, что и crtctl apply/remove, но без монитора.
//
//	boottest install   — поставить EDID для условного DP-2
//	boottest remove    — убрать
package main

import (
	"fmt"
	"os"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/system"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: boottest install|remove")
		os.Exit(2)
	}
	st, err := system.LoadStateFrom("/tmp/boottest-state.json")
	check(err)
	s := boot.Real(os.Stdout)
	fmt.Println("detected:", boot.Describe(s))
	conn := drm.Connector{Name: "DP-2", Card: "card0", PCI: "0000:00:02.0", SysPath: "/sys/class/drm/card0-DP-2"}
	switch os.Args[1] {
	case "install":
		m, _ := timing.FindDMT(1280, 1024, 85)
		raw, err := edid.Build(edid.BuildOptions{Name: "boottest", Modes: []timing.Mode{m}})
		check(err)
		check(system.InstallPersistent(st, system.PersistOptions{Conn: conn, EDID: raw, MonitorID: "CRT 1 0", Title: "boottest", AllKernels: true, Sys: s}))
		fmt.Println("installed:", st.Get("DP-2").EDIDFile, st.Get("DP-2").Method)
	case "remove":
		check(system.RemovePersistent(st, "DP-2", s, true))
		fmt.Println("removed")
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "boottest:", err)
		os.Exit(1)
	}
}
