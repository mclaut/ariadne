package i18n

import "testing"

func TestEveryLanguageHasEveryEnglishKey(t *testing.T) {
	for _, lang := range Available {
		for key := range table[EN] {
			if _, ok := table[lang][key]; !ok {
				t.Errorf("%s is missing %q", lang, key)
			}
		}
	}
}
