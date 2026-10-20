package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/sysenv"
)

// assumeYes — режим --yes: на все вопросы ответ по умолчанию, без ожидания ввода.
var assumeYes bool

// Ввод читает одна горутина: так ожидание с таймаутом не «съедает» ответ на следующий вопрос.
var (
	inputOnce sync.Once
	inputCh   = make(chan string)
)

func startInput() {
	inputOnce.Do(func() {
		go func() {
			r := bufio.NewReader(os.Stdin)
			for {
				s, err := r.ReadString('\n')
				if s != "" || err == nil {
					inputCh <- strings.TrimSpace(s)
				}
				if err != nil {
					close(inputCh)
					return
				}
			}
		}()
	})
}

// readLine ждёт строку; ok=false — ввод закончился.
func readLine() (string, bool) {
	startInput()
	s, ok := <-inputCh
	return s, ok
}

// readLineTimeout ждёт строку не дольше d.
func readLineTimeout(d time.Duration) (line string, got bool) {
	startInput()
	timer := time.After(d)
	select {
	case s, ok := <-inputCh:
		if !ok { // ввода нет (не терминал) — просто ждём до конца срока
			<-timer
		}
		return s, ok
	case <-timer:
		return "", false
	}
}

// askYesNo — вопрос да/нет; Enter — ответ по умолчанию. Понимает «да» на всех языках интерфейса.
func askYesNo(q string, def bool) bool {
	hint := T("[y/N]")
	if def {
		hint = T("[Y/n]")
	}
	fmt.Printf("%s %s ", q, hint)
	if assumeYes {
		fmt.Println()
		return def
	}
	a, ok := readLine()
	if !ok || a == "" {
		if !ok {
			fmt.Println()
		}
		return def
	}
	a = strings.ToLower(a)
	if isAnswer(a, i18n.N("yes")) {
		return true
	}
	if isAnswer(a, i18n.N("no")) {
		return false
	}
	return def
}

// isAnswer — слово (или его первая буква) на английском или на языке интерфейса.
func isAnswer(a, word string) bool {
	for _, w := range []string{word, strings.ToLower(T(word))} {
		if a == w || a == string([]rune(w)[:1]) {
			return true
		}
	}
	return false
}

// askConfirm — подтверждение с таймаутом: только явное «да» считается согласием.
// Если экран погас и человек ничего не видит, через seconds секунд будет «нет».
// --yes здесь не действует: подтвердить картинку может только человек.
func askConfirm(q string, seconds int) bool {
	fmt.Printf("%s %s ", q, T("[y/N, %d s]", seconds))
	a, got := readLineTimeout(time.Duration(seconds) * time.Second)
	if !got {
		fmt.Println()
		fmt.Println(T("No answer within %d seconds.", seconds))
		return false
	}
	return isAnswer(strings.ToLower(a), i18n.N("yes"))
}

// askChoice — номер варианта 1..n; 0 — отказ.
func askChoice(q string, n int) int {
	if assumeYes {
		return 0
	}
	for {
		fmt.Printf("%s [1-%d] ", q, n)
		a, ok := readLine()
		if !ok || a == "" {
			return 0
		}
		if v, err := strconv.Atoi(a); err == nil && v >= 1 && v <= n {
			return v
		}
		fmt.Println(T("Enter a number from 1 to %d.", n))
	}
}

// askFloat — число; ok=false, если ничего не ввели.
func askFloat(q string) (float64, bool) {
	if assumeYes {
		return 0, false
	}
	fmt.Printf("%s ", q)
	a, ok := readLine()
	if !ok || a == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(a, ",", "."), 64)
	return v, err == nil && v > 0
}

// reexecSudo перезапускает crtctl с теми же аргументами через sudo (язык сохраняется).
func reexecSudo(args []string) error {
	sudo := sysenv.HasCmd("sudo")
	if sudo == "" {
		return fmt.Errorf("%s", T("this command needs root, and sudo was not found: run it as root"))
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Println(T("Some steps need root; asking for the sudo password."))
	argv := append([]string{"sudo", exe, "--lang", i18n.Lang()}, args...)
	return syscall.Exec(sudo, argv, os.Environ())
}
