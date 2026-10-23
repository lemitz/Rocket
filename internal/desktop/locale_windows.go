//go:build windows

package desktop

import (
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
	"YOUR DEVICES, ONE ORBIT":                "ТВОИ УСТРОЙСТВА НА ОДНОЙ ОРБИТЕ",
	"FAST WI-FI TRANSFERS":                   "БЫСТРАЯ ПЕРЕДАЧА ПО WI-FI",
	"READY FOR LIFTOFF":                      "ГОТОВ К СТАРТУ",
	"Transfer files between your devices.":   "Передавай файлы между устройствами.",
	"Choose PC files and a phone to launch.": "Выберите файлы и телефон для отправки.",
	"TRANSFERRING FILES":                     "ПЕРЕДАЧА ФАЙЛОВ",
	"RECEIVING FROM PHONE":                   "ПОЛУЧЕНИЕ С ТЕЛЕФОНА",
	"SENDING TO PHONE":                       "ОТПРАВКА НА ТЕЛЕФОН",
	"Files in transit":                       "Файлы в пути",
	"Connecting…":                            "Подключение…",
	"INTERRUPTED":                            "ПРЕРВАНО",
	"Flight interrupted":                     "Полёт прерван",
	"You can try again.":                     "Можно попробовать ещё раз.",
	"DELIVERED":                              "ДОСТАВЛЕНО",
	"Flight complete":                        "Полёт завершён",
	"Files arrived safely.":                  "Файлы долетели без потерь.",
	"FILES":                                  "ФАЙЛОВ",
	"SIZE":                                   "ОБЪЁМ",
	"TIME":                                   "ВРЕМЯ",
	"AVG. SPEED":                             "СР. СКОРОСТЬ",
	"THIS COMPUTER":                          "ЭТОТ КОМПЬЮТЕР",
	"This PC":                                "Этот ПК",
	"Available on the local network":         "Доступен в локальной сети",
	"SERVER RUNNING":                         "СЕРВЕР ЗАПУЩЕН",
	"SERVER OFFLINE":                         "СЕРВЕР НЕ РАБОТАЕТ",
	"Network address not found":              "Адрес сети не найден",
	"+%d more addresses":                     "+%d других адреса",
	"DOWNLOADS FOLDER":                       "ПАПКА ЗАГРУЗОК",
	"No folder selected":                     "Папка не выбрана",
	"Waiting for connection":                 "Ожидание подключения",
	"Transferring":                           "Идёт передача",
	"Transfer complete":                      "Передача завершена",
	"Transfer interrupted":                   "Передача прервана",
	"Server offline":                         "Сервер не работает",
	"Ready to receive files":                 "Готов принимать файлы",
	"Open downloads  ↗":                      "Открыть загрузки  ↗",
	"Change folder  ···":                     "Сменить папку  ···",
	"Close Rocket  ×":                        "Закрыть Rocket  ×",
	"Saving folder…":                         "Сохраняем папку…",
	"Folder change unavailable":              "Смена папки недоступна",
	"Could not change folder: ":              "Не удалось сменить папку: ",
	"Could not change folder. Check access and try again.": "Не удалось сменить папку. Проверьте доступ и повторите попытку.",
	"Checksum mismatch.":                      "Контрольная сумма не совпала.",
	"Not enough space for the file.":          "Недостаточно места для файла.",
	"Folder updated for new transfers.":       "Папка обновлена для новых передач.",
	"Transfer interrupted. Please try again.": "Передача прервана. Попробуй ещё раз.",
	"No downloads folder selected.":           "Папка загрузок не выбрана.",
	"Could not open downloads folder.":        "Не удалось открыть папку загрузок.",
	"Saving downloads folder…":                "Сохраняем папку загрузок…",
	"Select Rocket downloads folder":          "Выберите папку загрузок Rocket",
	"1  SELECT FILES ON THIS PC":              "1  ВЫБЕРИТЕ ФАЙЛЫ НА ПК",
	"2  CHOOSE TARGET PHONE":                  "2  ВЫБЕРИТЕ ТЕЛЕФОН",
	"No files selected":                       "Файлы не выбраны",
	"%d files selected":                       "Выбрано файлов: %d",
	"Open Rocket on the target phone.":        "Откройте Rocket на нужном телефоне.",
	"Tap to refresh connected phones":         "Нажмите для обновления списка",
	"Ready to send":                           "Готов к отправке",
	"Waiting for phone":                       "Ожидание телефона",
	"Select files and a phone to start":       "Выберите файлы и телефон",
	"Select phone  ▾":                         "Выбрать телефон  ▾",
	"Choose files  ···":                       "Выбрать файлы  ···",
	"Launch  →":                               "Поехали  →",
	"Open  ↗":                                 "Открыть  ↗",
	"Change  ···":                             "Сменить  ···",
	"Select files to send":                    "Выберите файлы для отправки",
	"Could not show connected phones.":        "Не удалось показать телефоны.",
	"Looking for phones…":                     "Поиск телефонов…",
	"Queueing files…":                         "Постановка в очередь…",
	"Queued. Waiting for phone to connect.":   "В очереди. Ожидание телефона.",
	"Could not queue files: %v":               "Не удалось поставить файлы в очередь: %v",
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
