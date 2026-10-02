// Package i18n — серверные тексты (письма, пуши) из backend/locales (спека §6.9): ICU
// MessageFormat v1, как в клиентах; язык — users.locale, неизвестный — ru. Сообщения
// компилируются при загрузке: битое сообщение — отказ старта, а не письмо с дырой.
package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/kaptinlin/messageformat-go/mf1"
)

const Default = "ru"

var Supported = []string{"ru", "en"}

var ErrUnknownKey = errors.New("i18n: нет такого ключа")

type Catalog struct {
	msgs map[string]map[string]*mf1.CompiledMessage
}

func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{msgs: map[string]map[string]*mf1.CompiledMessage{}}
	for _, loc := range Supported {
		flat, err := Flatten(fsys, loc)
		if err != nil {
			return nil, err
		}
		mf, err := mf1.New(loc, &mf1.MessageFormatOptions{RequireAllArguments: true})
		if err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", loc, err)
		}
		c.msgs[loc] = make(map[string]*mf1.CompiledMessage, len(flat))
		for k, src := range flat {
			m, err := mf.Compile(src)
			if err != nil {
				return nil, fmt.Errorf("i18n: %s.json %s: %w", loc, k, err)
			}
			c.msgs[loc][k] = m
		}
	}
	return c, nil
}

// Flatten — <язык>.json в плоские ключи через точку; значения — только строки.
func Flatten(fsys fs.FS, locale string) (map[string]string, error) {
	data, err := fs.ReadFile(fsys, locale+".json")
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("i18n: %s.json: %w", locale, err)
	}
	out := map[string]string{}
	var walk func(prefix string, node map[string]any) error
	walk = func(prefix string, node map[string]any) error {
		for k, v := range node {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			switch v := v.(type) {
			case string:
				out[key] = v
			case map[string]any:
				if err := walk(key, v); err != nil {
					return err
				}
			default:
				return fmt.Errorf("i18n: %s.json %s: значение не строка", locale, key)
			}
		}
		return nil
	}
	return out, walk("", tree)
}

func (c *Catalog) Keys(locale string) []string {
	return slices.Sorted(maps.Keys(c.msgs[locale]))
}

func (c *Catalog) Text(locale, key string, args map[string]any) (string, error) {
	msgs, ok := c.msgs[locale]
	if !ok {
		msgs = c.msgs[Default]
	}
	m, ok := msgs[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	s, err := m.Format(args)
	if err != nil {
		return "", fmt.Errorf("i18n: %s: %w", key, err)
	}
	return s, nil
}
