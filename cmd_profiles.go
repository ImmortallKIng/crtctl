package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/ImmortallKIng/crtctl/internal/profile"
)

func cmdProfiles(args []string) error {
	var newID string
	fs := newFlags("profiles", "[ID]", `Monitor profiles: horizontal and vertical frequency ranges and video bandwidth.
Your own profiles are JSON files in ~/.config/crtctl/monitors (they override built-in
profiles with the same id). --new ID creates a template for your monitor.`)
	fs.StringVar(&newID, "new", "", "create a profile template with this id")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if newID != "" {
		return newProfile(newID)
	}
	all, err := profile.Load()
	if err != nil {
		return err
	}
	if len(pos) == 1 {
		p, err := profile.Get(pos[0])
		if err != nil {
			return err
		}
		row := func(k, v string) { fmt.Printf("  %s %s\n", padRight(T("%s:", k), 16), v) }
		fmt.Printf("%s — %s\n", p.ID, p.DisplayName())
		row(T("Horizontal"), T("%g–%g kHz", p.HMin, p.HMax))
		row(T("Vertical"), T("%g–%g Hz", p.VMin, p.VMax))
		if p.Bandwidth > 0 {
			row(T("Bandwidth"), T("%g MHz", p.Bandwidth))
		}
		if p.MaxRes != "" {
			row(T("Maximum"), p.MaxRes)
		}
		if len(p.Match) > 0 {
			row(T("EDID match"), strings.Join(p.Match, ", "))
		}
		if n := p.DisplayNotes(); n != "" {
			row(T("Notes"), n)
		}
		for _, s := range p.Sources {
			row(T("Source"), s)
		}
		row(T("File"), T(p.File))
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", "ID", T("MONITOR"), T("KHZ"), T("HZ"))
	for _, p := range all {
		fmt.Fprintf(tw, "%s\t%s\t%g–%g\t%g–%g\n", p.ID, p.DisplayName(), p.HMin, p.HMax, p.VMin, p.VMax)
	}
	tw.Flush()
	fmt.Println()
	fmt.Println(T("Details: crtctl profiles ID. Your own profile: crtctl profiles --new ID"))
	return nil
}

func newProfile(id string) error {
	if id == "" || strings.ContainsAny(id, `/\ `) {
		return fmt.Errorf("%s", T("invalid profile id %q: use letters, digits and dashes", id))
	}
	dir := profile.UserDir()
	if dir == "" {
		return fmt.Errorf("%s", T("could not find the configuration directory"))
	}
	path := filepath.Join(dir, id+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s", T("%s already exists", path))
	}
	p := profile.Monitor{
		ID: id, Name: "Monitor name", Match: []string{"VVV 1234"},
		HMin: 30, HMax: 70, VMin: 50, VMax: 160,
		Notes: "Take the values from the datasheet: horizontal and vertical frequency, video bandwidth. " +
			"match: vendor and model as crtctl info shows them; remove it if the monitor has no EDID.",
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println(T("Created %s — open it and fill it in from your monitor's datasheet.", path))
	return nil
}
