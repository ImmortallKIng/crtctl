// i18nlist печатает все переводимые строки модуля (для работы над переводами).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ImmortallKIng/crtctl/internal/i18n/extract"
)

func main() {
	root := flag.String("root", ".", "module root")
	flag.Parse()
	msgs, err := extract.Messages(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, m := range msgs {
		fmt.Printf("%q\n", m.ID)
	}
}
