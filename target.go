package main

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/ImmortallKIng/crtctl/internal/ddc"
	"github.com/ImmortallKIng/crtctl/internal/display"
	"github.com/ImmortallKIng/crtctl/internal/dpcd"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/sysenv"
	"github.com/ImmortallKIng/crtctl/internal/system"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

// limitFlags — общие флаги для ручного задания пределов.
type limitFlags struct {
	profile                      string
	hmin, hmax, vmin, vmax, pclk float64
}

func (lf *limitFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&lf.profile, "profile", "", "monitor profile (see crtctl profiles); by default it is found from the EDID")
	fs.Float64Var(&lf.hmin, "hmin", 0, "lowest horizontal frequency, kHz")
	fs.Float64Var(&lf.hmax, "hmax", 0, "highest horizontal frequency, kHz")
	fs.Float64Var(&lf.vmin, "vmin", 0, "lowest vertical refresh, Hz")
	fs.Float64Var(&lf.vmax, "vmax", 0, "highest vertical refresh, Hz")
	fs.Float64Var(&lf.pclk, "pclk", 0, "pixel clock limit (adapter), MHz")
}

func (lf *limitFlags) any() bool {
	return lf.profile != "" || lf.hmin+lf.hmax+lf.vmin+lf.vmax+lf.pclk > 0
}

// target — всё, что известно о коннекторе.
type target struct {
	Conn       drm.Connector
	KernelEDID *edid.EDID // то, что видит ядро (может быть подменённым)
	RealRaw    []byte
	RealEDID   *edid.EDID // прочитанный напрямую по DDC
	RealErr    error
	Profile    *profile.Monitor
	ProfileSrc string
	Limits     profile.Limits
	State      *system.ConnState
	DPCD       *dpcd.Info
	DPCDErr    error
	Override   string // файл из drm.edid_firmware для этого коннектора (текущая загрузка)
	Runtime    []byte // EDID, подложенный через debugfs (только под root)
	KMS        *drm.KMSState
	Driver     string // amdgpu, i915, nvidia, nouveau...
	Adapter    adapterKind
	// Unverified — монитор не отдаёт EDID, а ядро использует подложенный: что подключено
	// на самом деле, неизвестно, и пределы из подложенного EDID брать нельзя.
	Unverified bool
}

// KernelOverridden — EDID ядра подложен (параметром ядра, через debugfs или crtctl).
func (t *target) KernelOverridden() bool {
	if t.KernelEDID == nil {
		return false
	}
	if t.Override != "" || t.Runtime != nil {
		return true
	}
	if t.RealRaw != nil {
		return !bytes.Equal(t.KernelEDID.Raw, t.RealRaw)
	}
	if t.State != nil && t.State.EDIDSHA256 != "" && system.SHA256(t.KernelEDID.Raw) == t.State.EDIDSHA256 {
		return true
	}
	return false
}

// Monitor — EDID, по которому монитор опознан надёжно: настоящий по DDC или
// EDID ядра, если ядро прочитало его само. nil — опознать нельзя.
func (t *target) Monitor() *edid.EDID {
	if t.RealEDID != nil {
		return t.RealEDID
	}
	if t.KernelEDID != nil && !t.KernelOverridden() {
		return t.KernelEDID
	}
	return nil
}

func loadTarget(name string, lf *limitFlags) (*target, error) {
	c, err := drm.Find(name)
	if err != nil {
		return nil, err
	}
	t := &target{Conn: c}
	if len(c.EDID) > 0 {
		t.KernelEDID, _ = edid.Parse(c.EDID)
	}
	t.Driver = sysenv.Driver(c.Card)
	if c.Connected() && c.DDCBus < 0 {
		// NVIDIA не связывает коннектор с шиной DDC — ищем среди шин видеокарты
		var want []byte
		if len(c.EDID) > 0 {
			want = c.EDID
		}
		if bus, _ := ddc.FindBus(c.PCI, want, connectedOnCard(c.Card) == 1); bus >= 0 {
			c.DDCBus = bus
			t.Conn.DDCBus = bus
		}
	}
	if c.Connected() && c.DDCBus >= 0 {
		t.RealRaw, t.RealErr = ddc.ReadEDID(c.DDCBus)
		if t.RealErr == nil {
			t.RealEDID, t.RealErr = edid.Parse(t.RealRaw)
		}
		if t.RealErr != nil {
			t.RealRaw = nil
		}
	}
	if st, err := system.LoadState(); err == nil {
		t.State = st.Get(c.Name)
	}
	t.Override = system.RunningEDIDOverrides()[c.Name]
	if os.Geteuid() == 0 {
		t.Runtime = system.RuntimeOverride(c)
		if c.Connected() && c.AuxDev != "" {
			t.DPCD, t.DPCDErr = dpcd.Read(c.AuxDev)
			if t.DPCD != nil && t.DPCD.Branch {
				cacheAdapter(c.Name, t.DPCD)
			}
		}
	}
	t.KMS, _ = drm.ReadKMS(c)
	t.Unverified = t.RealEDID == nil && t.KernelOverridden()
	if err := t.resolveLimits(lf); err != nil {
		return nil, err
	}
	return t, nil
}

// cacheAdapter запоминает регистры переходника, чтобы команды без root знали его предел.
func cacheAdapter(conn string, in *dpcd.Info) {
	st, err := system.LoadState()
	if err != nil {
		return
	}
	c := st.Conn(conn)
	if c.AdapterDPCD == in.Hex() && c.AdapterPclk == in.HardLimitMHz() {
		return
	}
	// AdapterInfo — только для чтения человеком; в выводе описание строится из регистров
	c.AdapterPclk, c.AdapterInfo, c.AdapterDPCD = in.HardLimitMHz(), "", in.Hex()
	_ = st.Save()
}

// savedAdapter — переходник по сохранённым регистрам (команды без root).
func (t *target) savedAdapter() *dpcd.Info {
	if t.State == nil || t.State.AdapterDPCD == "" {
		return nil
	}
	in, err := dpcd.DecodeHex(t.State.AdapterDPCD)
	if err != nil {
		return nil
	}
	return in
}

func adapterSummary(in *dpcd.Info) string {
	s := T("DP %s → %s, %d lanes × %s Gbit/s", in.Rev, in.PortType, in.Lanes, dpcd.RateName(in.LinkRateCode))
	if in.Downspread {
		s += ", " + T("downspread")
	}
	if in.OUI != "" || in.DeviceID != "" {
		s += ", " + T("chip %s %s", in.OUI, in.DeviceID)
	} else {
		s += ", " + T("no vendor ID")
	}
	return s
}

func (t *target) resolveLimits(lf *limitFlags) error {
	l := profile.NewLimits()
	mon := t.Monitor()
	switch {
	case lf != nil && lf.profile != "":
		p, err := profile.Get(lf.profile)
		if err != nil {
			return err
		}
		t.Profile, t.ProfileSrc = &p, T("chosen with --profile")
	case mon != nil:
		if p, ok := profile.Match(mon.Vendor, mon.Product); ok {
			t.Profile, t.ProfileSrc = &p, T("matched by EDID")
		}
	case t.Unverified && t.State != nil && t.State.Profile != "" && t.State.MonitorNoDDC:
		// EDID ставился для монитора без DDC с явно выбранным профилем — доверяем этому выбору
		if p, err := profile.Get(t.State.Profile); err == nil {
			t.Profile, t.ProfileSrc = &p, T("from the installed EDID (the monitor has no DDC, so it cannot be verified)")
		}
	}
	switch {
	case t.Profile != nil:
		l.FromMonitor(*t.Profile, T("profile %s", t.Profile.ID))
	case mon != nil && mon.Range != nil && t.RealEDID != nil:
		r := mon.Range
		l.FromMonitor(profile.Monitor{HMin: float64(r.HMin), HMax: float64(r.HMax), VMin: float64(r.VMin), VMax: float64(r.VMax)}, T("ranges from the monitor's EDID"))
		if r.PclkMax > 0 {
			l.Bandwidth = float64(r.PclkMax)
			l.From["bw"] = T("ranges from the monitor's EDID")
		}
	case mon != nil && mon.Digital:
		// Цифровой приёмник (ЖК, конвертер HDMI→AV): развёртка CRT к нему не относится
	case mon != nil && len(mon.Modes()) > 0:
		// Нет диапазонов — берём то, что монитор заявил о себе: это заведомо безопасно
		var vmax float64
		for _, m := range mon.Modes() {
			vmax = math.Max(vmax, m.VFreq())
		}
		l.FromMonitor(profile.Monitor{HMin: 30, HMax: math.Floor(mon.MaxHFreq()*100) / 100, VMin: 50, VMax: math.Floor(vmax)},
			T("estimated from the modes the monitor declares"))
	}
	t.Adapter = t.detectAdapter()
	if t.Adapter.PclkMHz > 0 {
		l.SetPclk(t.Adapter.PclkMHz, t.Adapter.Source)
	}
	if lf != nil {
		l.Override(lf.hmin, lf.hmax, lf.vmin, lf.vmax, lf.pclk)
	}
	t.Limits = l
	return nil
}

// unverifiedHint — что сказать, когда монитор не опознать.
func (t *target) unverifiedHint() string {
	s := T("The monitor does not send an EDID, and the kernel uses a replaced one, so crtctl cannot tell which monitor is connected.")
	if t.State != nil && t.State.MonitorTitle != "" {
		s += " " + T("The installed EDID was made for «%s».", t.State.MonitorTitle)
		if t.State.Profile != "" {
			s += " " + T("If that monitor is connected, add --profile %s.", t.State.Profile)
		}
	}
	return s
}

// pickConnector выбирает коннектор, если пользователь его не указал.
func pickConnector(args []string) (string, []string, error) {
	if len(args) > 0 && !looksLikeMode(args[0]) {
		return args[0], args[1:], nil
	}
	all, err := drm.List()
	if err != nil {
		return "", nil, err
	}
	var conn []string
	for _, c := range all {
		if c.Connected() {
			conn = append(conn, c.Name)
		}
	}
	switch len(conn) {
	case 1:
		return conn[0], args, nil
	case 0:
		return "", nil, fmt.Errorf("%s", T("no monitor is connected"))
	}
	// Несколько мониторов: если похожий на CRT среди них один, берём его
	var crt []string
	for _, n := range conn {
		if t, err := loadTarget(n, &limitFlags{}); err == nil && isCRTCandidate(t) {
			crt = append(crt, n)
		}
	}
	if len(crt) == 1 {
		fmt.Fprintln(os.Stderr, "· "+T("Using %s, the only output that looks like a CRT (name another output to change that).", crt[0]))
		return crt[0], args, nil
	}
	return "", nil, fmt.Errorf("%s", T("name the connector, several are connected: %s", strings.Join(conn, ", ")))
}

func looksLikeMode(s string) bool {
	_, err := timing.ParseRequest(s)
	return err == nil
}

// classify — к какому стандарту относится режим.
func classify(m timing.Mode) string {
	if d, ok := timing.FindDMT(m.HActive, m.VActive, m.VFreq()); ok && sameTiming(d, m) {
		return d.Source
	}
	hz := math.Round(m.VFreq())
	for _, try := range []struct {
		name string
		m    timing.Mode
	}{
		{"CVT", timing.CVT(m.HActive, m.VActive, hz, false)},
		{"CVT-RB", timing.CVT(m.HActive, m.VActive, hz, true)},
		{"GTF", timing.GTF(m.HActive, m.VActive, hz)},
	} {
		if sameTiming(try.m, m) {
			return try.name
		}
	}
	return T("custom")
}

func sameTiming(a, b timing.Mode) bool {
	return a.HActive == b.HActive && a.HSyncStart == b.HSyncStart && a.HSyncEnd == b.HSyncEnd && a.HTotal == b.HTotal &&
		a.VActive == b.VActive && a.VSyncStart == b.VSyncStart && a.VSyncEnd == b.VSyncEnd && a.VTotal == b.VTotal &&
		absInt(a.PixelClock-b.PixelClock) <= 10
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// verdict — короткая пометка для таблиц.
func verdict(l profile.Limits, m timing.Mode) string {
	if !l.Known() && l.PclkMax == 0 {
		return ""
	}
	ps := l.Check(m)
	if profile.HasHard(ps) {
		return "✗ " + T("out of limits")
	}
	if len(ps) > 0 {
		return "⚠ " + shortSoft(ps)
	}
	return "✓"
}

func shortSoft(ps []profile.Problem) string {
	for _, p := range ps {
		switch p.Kind {
		case "bw":
			return T("softer picture (above bandwidth)")
		case "rb":
			return T("reduced blanking")
		}
	}
	return ps[0].Text
}

func modeLine(m timing.Mode) string {
	return fmt.Sprintf("%-11s %s", fmt.Sprintf("%dx%d", m.HActive, m.VActive),
		T("%7.2f Hz %7.2f kHz %7.2f MHz", m.VFreq(), m.HFreq(), m.PixelClockMHz()))
}

// monitorTitle — имя монитора для заголовков: из EDID, а если там только коды — из профиля.
func (t *target) monitorTitle() string {
	m := t.Monitor()
	switch {
	case m == nil && t.Profile != nil:
		return t.Profile.DisplayName() + " (" + T("not verified") + ")"
	case m == nil:
		return T("unknown monitor")
	case m.Name == "" && t.Profile != nil:
		return t.Profile.DisplayName() + " (" + m.Title() + ")"
	}
	return m.Title()
}

// padRight дополняет строку пробелами до ширины в символах (а не байтах, как %-Ns).
func padRight(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// adapterKind — что стоит между видеокартой и CRT и какой у этого предел частоты пикселей.
type adapterKind struct {
	Kind    string // dp-vga, dp-unknown, tmds-vga, native, digital, ""
	Desc    string
	PclkMHz float64
	Source  string
	Info    *dpcd.Info
}

// analogSink — по EDID монитор аналоговый (CRT или другой VGA-монитор).
func (t *target) analogSink() (analog, known bool) {
	e := t.Monitor()
	if e == nil {
		e = t.KernelEDID // подложенный нами EDID собирается на основе аналогового
	}
	if e == nil {
		return false, false
	}
	return !e.Digital, true
}

func connType(name string) string {
	for _, p := range []string{"eDP", "DP", "HDMI-A", "HDMI-B", "DVI-D", "DVI-I", "DVI-A", "VGA"} {
		if strings.HasPrefix(name, p+"-") {
			return p
		}
	}
	return ""
}

func (t *target) detectAdapter() adapterKind {
	analog, known := t.analogSink()
	switch connType(t.Conn.Name) {
	case "DP", "eDP":
		in := t.DPCD
		src := T("adapter, DPCD registers")
		if in == nil {
			in, src = t.savedAdapter(), T("adapter, saved DPCD registers")
		}
		switch {
		case in != nil && in.Branch:
			return adapterKind{Kind: "dp-vga", Desc: adapterSummary(in), PclkMHz: in.HardLimitMHz(), Source: src, Info: in}
		case in != nil:
			return adapterKind{Kind: "digital", Desc: T("DP %s, %d lanes × %s Gbit/s, no adapter", in.Rev, in.Lanes, dpcd.RateName(in.LinkRateCode)), Info: in}
		case analog || !known:
			return adapterKind{Kind: "dp-unknown", Desc: T("DP→VGA adapter (its registers are not available)"),
				PclkMHz: 165, Source: T("cautious assumption for an adapter without data; set the real limit with --pclk")}
		}
	case "HDMI-A", "HDMI-B", "DVI-D":
		if analog || !known {
			return adapterKind{Kind: "tmds-vga", Desc: T("%s→VGA converter", connType(t.Conn.Name)),
				PclkMHz: 165, Source: T("single-link TMDS: drivers allow at most 165 MHz towards a VGA converter")}
		}
		return adapterKind{Kind: "digital"}
	case "VGA", "DVI-I", "DVI-A":
		return adapterKind{Kind: "native", Desc: T("analog output of the graphics card"),
			PclkMHz: 400, Source: T("graphics card DAC (typically 400 MHz)")}
	}
	return adapterKind{}
}

// nvidiaXOutput — имя выхода в запущенном X-сервере NVIDIA для этого коннектора ("" — не нужно
// или не найдено). X-драйвер NVIDIA видит настоящий EDID монитора, по нему и сопоставляем.
func nvidiaXOutput(t *target) string {
	if t.Driver != "nvidia" || os.Geteuid() != 0 {
		return ""
	}
	want := t.RealRaw
	if want == nil {
		want = t.Conn.EDID
	}
	for _, x := range sysenv.XorgServers() {
		outs, err := display.XOutputsFor(x.Display, x.Auth)
		if err != nil {
			continue
		}
		if o := display.MatchXOutput(outs, t.Conn.Name, t.Conn.ID, want); o != nil {
			return o.Name
		}
	}
	return ""
}

func connectedOnCard(card string) int {
	all, _ := drm.List()
	n := 0
	for _, c := range all {
		if c.Card == card && c.Connected() {
			n++
		}
	}
	return n
}
