# Сборка, проверки, диагностика

Команды ниже исполняются в Windows PowerShell; корень проекта — `Z:\opencode\Rocket`. Проект — Git-репозиторий (`main`); `git.exe` лежит в `C:\Program Files\Git\cmd` и не в PATH. Не считать `rocket.exe` и `android/app/build/` исходниками (они в `.gitignore`).

## Windows Go

Требуется установленный Go 1.25+ (проверялся `go1.25.5 windows/amd64`). В `go.mod` только `module rocket`, внешних Go-модулей нет.

```powershell
# workdir: Z:\opencode\Rocket
go test -race ./...
go vet ./...
go build -trimpath -ldflags='-s -w' -o rocket.exe ./cmd/rocket
.\rocket.exe help
.\rocket.exe settings show
.\rocket.exe              # native GUI + сервер, закрыть окно для остановки
.\rocket.exe daemon       # консольная диагностика; вместо GUI, НЕ одновременно с ним
.\rocket.exe ping         # локальная диагностическая команда, localhost
```

Сборка перезапишет `rocket.exe`: сначала **закрой действующий Rocket** (иначе Windows может заблокировать EXE). Если при GUI запуске уже занят TCP `42070`, появится ошибка; окно *или* `daemon` запускается один раз на ПК. `gui --dir 'D:\Test'`/`daemon --dir 'D:\Test'` задают папку разово (она должна существовать), GUI-настройка папки сохраняется для новых соединений. `-host 127.0.0.1` полезен для диагностики без LAN. GUI без аргументов скрывает созданную Explorer консоль; `daemon` оставляет консоль открытой.

Go-тесты: конфигурация, Windows no-overwrite rename, CRC-кадры, запрет `list`/`get`, `poll`/`offer-get`/`offer-done`, **успех исходящего сеанса только после `offer-done`**, `outbox.Drop`, коллизии и отменённые PUT, статистика сеансов; для окна ПК — состояния сцены, поведение слайдера, переходы сеансов, плюральные формы, RU/EN-строки, разметка структур Win32.

### Вид окна ПК без запуска (снимки)

```powershell
$env:ROCKET_SNAPSHOT_DIR = 'C:\path\to\snap'; go test ./internal/desktop -run TestRenderStates
```

Пишет PNG всех состояний (idle, выбор, слайдер, старт, полёт, вылет, дым, результат, ошибка, лист адресата, настройки) для RU и EN. Окно не нужно: рисует программный растеризатор на поддельных часах. Просматривай файлы **после** завершения команды. `go test ./internal/desktop -run TestFrameCost -v` печатает стоимость кадра в памяти (бюджет 8,3 мс для 120 Гц); это не число показанных кадров.

Интерактивный тест окна: `$env:ROCKET_GUI_SMOKE='1'; go test ./internal/desktop -run TestInteractive -v` (закрой реальное окно Rocket заранее).

### Иконка, ресурсы EXE и ассеты

- Источник — `assets/icon-source.png` (1024×1024, PNG из `icon.webp` пользователя). `go run ./tools/genassets` (из корня) создаёт `internal/desktop/assets/rocket.png`, `android/.../drawable-nodpi/{rocket,ic_launcher_foreground,ic_launcher_monochrome}.png`, `assets/icon-circle.png` и `cmd/rocket/rocket.ico`. Он же печатает координаты носа и пламени; они продублированы константами в `scene_windows.go` и `LaunchView.kt`.
- Иконка и манифест вшиваются в EXE: `cd cmd\rocket` и `windres --target=pe-x86-64 -O coff -i rocket.rc -o rocket_windows_amd64.syso` (`C:\msys64\mingw64\bin` в PATH). Файл `.syso` лежит в Git, `go build` подхватывает его сам.

### Сквозная проверка на эмуляторе (как делалось)

1. Закрыть Rocket, собрать EXE и APK, запустить `rocket.exe gui --dir C:\тестовая\папка`; `emulator -avd Medium_Phone_API_36.0`; `adb install -r app-debug.apk`.
2. На эмуляторе ПК виден как `10.0.2.2` (в настройках телефона). Файл в `/sdcard/Download`; выбор через системный пикер (`adb shell input tap`/`uiautomator dump`) и слайдер `adb shell input swipe`.
3. Для ПК → телефон не отправлять клавиши и клики в активное окно. Окно управляется сообщениями прямо в HWND Rocket: `VK_RETURN` (`WM_KEYDOWN`) открывает выбор файлов, имя вводится `WM_SETTEXT` в поле диалога, «Открыть» — `BM_CLICK`, слайдер — `WM_LBUTTONDOWN`/`WM_MOUSEMOVE`/`WM_LBUTTONUP` с клиентскими координатами. Снимок окна — `PrintWindow`.
4. Сверять SHA-256 исходника и сохранённой копии, а также согласованность статистики на обеих сторонах. После теста удалить тестовые файлы из `Download` эмулятора и закрыть Rocket.
## Android Kotlin

`Z:\SDK` содержит SDK и `platform-tools/adb.exe`, platform API 36; установлен JDK 21. `android/gradle/wrapper` содержит Gradle 8.13; root `android/build.gradle.kts` — AGP 8.13.1 и Kotlin Android 2.1.0. `android/app/build.gradle.kts`: compile/target SDK 36, min SDK 34, bytecode Java/Kotlin 17, `applicationId=dev.rocket.android`, debug APK версия 0.1. В APK нет Compose, AndroidX и сторонних runtime-зависимостей.

```powershell
# workdir: Z:\opencode\Rocket\android
.\gradlew.bat :app:assembleDebug --offline --no-daemon
powershell -NoProfile -File .\tests\run-smoke.ps1

# артефакт: app\build\outputs\apk\debug\app-debug.apk
& 'Z:\SDK\platform-tools\adb.exe' devices
& 'Z:\SDK\platform-tools\adb.exe' install -r '.\app\build\outputs\apk\debug\app-debug.apk'
```

`android/local.properties` содержит `sdk.dir=Z:/SDK` **только для этой машины**, файл игнорируется Android `.gitignore`. В другом окружении настрой `ANDROID_HOME`/`ANDROID_SDK_ROOT` или свой путь в `local.properties`. `--offline` возможен лишь при заранее скачанных Gradle/AGP/Kotlin артефактах; на новой машине первый sync может требовать сеть. `tests/run-smoke.ps1` проверяет **только парсер UDP-обнаружения** на JVM после build; тестов Android `src/test`/`androidTest` пока нет.

## Ручной сквозной тест

1. На ПК запусти один сервер Rocket (для проверки без пользовательских файлов — `rocket.exe gui --dir 'C:\absolute\existing\test-dir'`). На Android выбери этот ПК через скрытые настройки. На этом стенде Android emulator обычно видит хост по `10.0.2.2`; на реальном телефоне нужен **LAN IPv4 ПК**, а не 127.0.0.1. UDP может блокироваться AP/брандмауэром; ручной IP — резерв.
2. **ПК → телефон:** на ПК выбери локальный файл, дождись появления открытого телефона в меню, выбери его и нажми Launch. На телефоне ничего из ПК-файлов не выбирай — проверь автоматическую загрузку в Download, совпадение длины/хэша, успешную статистику и что в `Download` нет незавершённых документов.
3. **Телефон → ПК:** на телефоне через пикер выбери файл или отправь из Галереи Share-интентом. Убедись, что до Launch файл **не появился** на ПК. Нажми Launch, проверь результат в настроенной папке ПК и имя при коллизии. Сымитируй отмену и повреждённую контрольную сумму на тестовом соединении — завершённого файла не должно быть.
4. Проверить отдельно на **двух физических ПК и двух телефонах**, что пользователь выбирает именно нужное устройство, один телефон не видит чужие предложения, пачка нескольких файлов не портит статистику. Проверить OEM Share URI и длительный большой файл на realme GT 6T и Nothing Phone (3a), сценарии onStop, недостаток места, отключение сети и повтор.

Ранее выполнялась сквозная проверка обоих направлений с настоящими ПК GUI и Android API 36 emulator: локальный README.md с ПК попал в `Download`, затем после выбора на телефоне и Launch дошёл в тестовую папку ПК. Это **не замена** проверкам железа, Wi-Fi 6 и 120 fps.

## Сеть/брандмауэр

TCP `42070`, UDP `42069` по умолчанию. Если обнаружение/соединение не работает, проверь, что профиль сети Windows **Private**, устройства в одной подсети и Wi-Fi AP не включает client isolation. Из **терминала администратора** на каждом ПК один раз:

```powershell
.\rocket.exe firewall install
# эквивалент: входящие TCP 42070 и UDP 42069,
# только profile=Private и remoteip=LocalSubnet
```

Обычный запуск Rocket ничего в брандмауэре не меняет; окно показывает проверку правил в настройках, а кнопка «Исправить» запускает эту же команду с запросом UAC. Команда заменяет старые правила `Rocket TCP`/`Rocket UDP`, а не дублирует их. В консольном режиме `rocket.exe daemon` пишет реальные адреса интерфейсов; GUI показывает адреса ПК в настройках («Этот компьютер»). `-port` меняет TCP listener для диагностики, но Android/UDP discovery/команда firewall жёстко используют 42070/42069: нестандартный порт **не совместим с текущим APK**.

## Фактическое состояние проверок при подготовке handoff

Выполнены: `go vet ./...`, `go test -race ./...`, `gradlew.bat :app:assembleDebug --offline --no-daemon`, `android/tests/run-smoke.ps1` (всё успешно); сквозные передачи в обе стороны на эмуляторе API 36 с настоящим `rocket.exe` (SHA-256 совпал); снимки состояний ПК. Не проверялись реальные телефоны, два ПК, Wi-Fi 6, масштаб Windows выше 100% и 120 показанных кадров/с (см. [OPEN_ISSUES](OPEN_ISSUES.md)). Тестовые файлы с эмулятора удалены, `rocket.exe` и эмулятор закрыты.