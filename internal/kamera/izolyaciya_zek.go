//go:build unix

package kamera

import (
	"errors"
	"fmt"
)

// zekOpts — аргументы служебного режима __zek.
type zekOpts struct {
	as, cpu   uint64
	odinochka bool
	set       bool
	bezDoma   bool
	proverka  bool
	dir       string
	zapis     []string
	tmp       string
	path      string
	argv      []string
}

// args превращает настройки в аргументы командной строки.
func (o zekOpts) args() []string {
	var a []string
	if o.as > 0 {
		a = append(a, "--as", fmt.Sprint(o.as))
	}
	if o.cpu > 0 {
		a = append(a, "--cpu", fmt.Sprint(o.cpu))
	}
	if o.odinochka {
		a = append(a, "--odinochka", "--dir", o.dir)
		if o.set {
			a = append(a, "--set")
		}
		if o.bezDoma {
			a = append(a, "--bez-doma")
		}
		for _, p := range o.zapis {
			a = append(a, "--zapis", p)
		}
		if o.tmp != "" {
			a = append(a, "--tmp", o.tmp)
		}
		if o.proverka {
			a = append(a, "--proverka")
		}
	}
	a = append(a, "--", o.path)
	return append(a, o.argv...)
}

func parseZek(args []string) (zekOpts, error) {
	var o zekOpts
	val := func(i int) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("нет значения у %s", args[i])
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--as", "--cpu":
			v, err := val(i)
			if err != nil {
				return o, err
			}
			var n uint64
			if _, err := fmt.Sscan(v, &n); err != nil {
				return o, fmt.Errorf("%s: %w", a, err)
			}
			if a == "--as" {
				o.as = n
			} else {
				o.cpu = n
			}
			i++
		case "--dir", "--zapis", "--tmp":
			v, err := val(i)
			if err != nil {
				return o, err
			}
			switch a {
			case "--dir":
				o.dir = v
			case "--zapis":
				o.zapis = append(o.zapis, v)
			default:
				o.tmp = v
			}
			i++
		case "--odinochka":
			o.odinochka = true
		case "--set":
			o.set = true
		case "--bez-doma":
			o.bezDoma = true
		case "--proverka":
			o.proverka = true
		case "--":
			rest := args[i+1:]
			if len(rest) < 1 {
				return o, errors.New("нет команды")
			}
			o.path = rest[0]
			o.argv = rest[1:]
			return o, nil
		default:
			return o, fmt.Errorf("непонятный аргумент %s", a)
		}
	}
	return o, errors.New("нет «--» и команды")
}
