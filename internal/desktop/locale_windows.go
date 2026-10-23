//go:build windows

package desktop

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	preferredUILanguages = kernel32.NewProc("GetUserPreferredUILanguages")
	defaultUILanguage    = kernel32.NewProc("GetUserDefaultUILanguage")
)

// Select the FIRST Windows UI preference, not the keyboard layout or the
// region. Unsupported UI languages and API failures fall back to English.
// IsRussianUI reports whether Windows' preferred UI language is Russian.
// The same choice is used for desktop copy and startup errors.
func IsRussianUI() bool { return preferredRussian() }

func preferredRussian() bool {
	const muiLanguageName = 8
	var count, length uint32
	ok, _, _ := preferredUILanguages.Call(muiLanguageName, uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&length)))
	if ok != 0 && length > 0 && length < 4096 {
		buf := make([]uint16, length)
		ok, _, _ = preferredUILanguages.Call(muiLanguageName, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&length)))
		if ok != 0 && count > 0 {
			first := syscall.UTF16ToString(buf)
			return isRussianLanguage(first)
		}
	}
	lang, _, _ := defaultUILanguage.Call()
	return lang&0x3ff == 0x19 // LANG_RUSSIAN
}
func isRussianLanguage(tag string) bool {
	tag = strings.ToLower(tag)
	return tag == "ru" || strings.HasPrefix(tag, "ru-")
}

// English keys provide a complete fallback. Only fixed UI copy belongs here;
// file names, addresses, paths, transfer errors and device names are user data.
var russianText = map[string]string{
	"Choose files":                   "Выбрать файлы",
	"or drop files here":             "или перетащите файлы сюда",
	"Let's go!":                      "Поехали!",
	"To":                             "На",
	"Open Rocket on the phone":       "Откройте Rocket на телефоне",
	"Choose a phone":                 "Выберите телефон",
	"Waiting for the phone…":         "Ждём телефон…",
	"Sending…":                       "Отправка…",
	"Receiving…":                     "Получение…",
	"Delivered!":                     "Долетели!",
	"Files sent":                     "Файлы отправлены",
	"Files received":                 "Файлы получены",
	"Files":                          "Файлов",
	"Size":                           "Объём",
	"Time":                           "Время",
	"Avg. speed":                     "Ср. скорость",
	"OK":                             "Ок",
	"Open folder":                    "Открыть папку",
	"Didn't make it":                 "Не долетели",
	"Your files are still selected.": "Файлы всё ещё выбраны.",
	"The phone did not respond. Open Rocket on it and try again.": "Телефон не отозвался. Откройте на нём Rocket и повторите.",
	"Could not queue the files: %v":                               "Не удалось поставить файлы в очередь: %v",
	"Settings":                                                    "Настройки",
	"Receive folder":                                              "Папка приёма",
	"Not set":                                                     "Не задана",
	"Change":                                                      "Изменить",
	"Open":                                                        "Открыть",
	"This computer":                                               "Этот компьютер",
	"This PC":                                                     "Этот ПК",
	"No network address found":                                    "Сетевой адрес не найден",
	"Connection check":                                            "Проверка связи",
	"Rocket server":                                               "Сервер Rocket",
	"Device discovery":                                            "Поиск устройств",
	"Firewall":                                                    "Брандмауэр",
	"Running":                                                     "Работает",
	"Not running":                                                 "Не работает",
	"Unavailable":                                                 "Недоступен",
	"Checking…":                                                   "Проверяю…",
	"Rules found":                                                 "Правила есть",
	"Rules missing":                                               "Правил нет",
	"Fix":                                                         "Исправить",
	"Recheck":                                                     "Проверить",
	"Phones":                                                      "Телефоны",
	"No phones yet. Open Rocket on a phone.":                      "Пока нет телефонов. Откройте Rocket на телефоне.",
	"%d s ago":                                                    "%d с назад",
	"No encryption or passwords: use Rocket only on a network you trust.": "Без шифрования и паролей: используйте Rocket только в доверенной сети.",
	"Select files to send":                                     "Выберите файлы для отправки",
	"Select the Rocket receive folder":                         "Выберите папку приёма Rocket",
	"Only regular files can be sent":                           "Можно отправлять только обычные файлы",
	"Too many files (up to 256)":                               "Слишком много файлов (максимум 256)",
	"Folder changed":                                           "Папка изменена",
	"Could not change the folder. Check access and try again.": "Не удалось сменить папку. Проверьте доступ и повторите.",
	"Could not open the folder":                                "Не удалось открыть папку",
	"Could not start the firewall fix":                         "Не удалось запустить исправление брандмауэра",
	"Checksum mismatch.":                                       "Контрольная сумма не совпала.",
	"Not enough space for the file.":                           "Недостаточно места для файла.",
	"Transfer interrupted. Please try again.":                  "Передача прервана. Попробуйте ещё раз.",
}

func (w *window) tr(english string) string {
	if w.russian {
		if translated, ok := russianText[english]; ok {
			return translated
		}
	}
	return english
}

func (w *window) transferError(message string) string {
	if !w.russian && message != "" {
		return message
	}
	if w.russian {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "checksum") {
			return w.tr("Checksum mismatch.")
		}
		if strings.Contains(lower, "no space") || strings.Contains(lower, "disk full") {
			return w.tr("Not enough space for the file.")
		}
	}
	return w.tr("Transfer interrupted. Please try again.")
}

// filesLabel is "1 file", "2 files" / "1 файл", "2 файла", "5 файлов".
func (w *window) filesLabel(n int) string {
	if !w.russian {
		if n == 1 {
			return "1 file"
		}
		return fmt.Sprintf("%d files", n)
	}
	last, tens := n%10, n%100
	switch {
	case tens >= 11 && tens <= 14, last == 0, last >= 5:
		return fmt.Sprintf("%d файлов", n)
	case last == 1:
		return fmt.Sprintf("%d файл", n)
	}
	return fmt.Sprintf("%d файла", n)
}

func (w *window) byteSize(n int64) string {
	label := byteSize(n)
	if !w.russian {
		return label
	}
	return strings.NewReplacer(" PB", " ПБ", " TB", " ТБ", " GB", " ГБ", " MB", " МБ", " KB", " КБ", " B", " Б").Replace(label)
}
func (w *window) duration(d time.Duration) string {
	label := durationLabel(d)
	if w.russian {
		return strings.NewReplacer("h", "ч", "m", "м", "s", "с").Replace(label)
	}
	return label
}
func (w *window) speed(bytes int64, d time.Duration) string {
	if bytes <= 0 || d <= 0 {
		return "—"
	}
	suffix := "/s"
	if w.russian {
		suffix = "/с"
	}
	return w.byteSize(int64(float64(bytes)/d.Seconds())) + suffix
}
