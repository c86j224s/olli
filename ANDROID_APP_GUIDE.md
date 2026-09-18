# O.L.L.I. Android Mobile 가이드

O.L.L.I. Mobile은 Android 기기에서 Ollama나 파일 도구를 직접 실행하지 않는 원격 관찰·승인 클라이언트입니다. O.L.L.I. Controller에 HTTPS로 연결해 실행을 시작하거나 취소하고, Development Graph·Live Events·AI Gateway 상태를 확인하며, 민감 도구의 permission 결정을 전달합니다.

## 구성

```text
Android App
    │ HTTPS + Bearer token
    ▼
cmd/olli-controller
    ├── controller.Service
    ├── agent.Agent + Permission Engine
    ├── DevelopmentTeamRunner
    ├── AI Gateway
    └── runstate Store + JSONL replay
```

Android 앱은 Controller API 외의 Ollama 노드나 workspace 파일에 직접 연결하지 않습니다. 파일 도구와 command sandbox는 Controller 머신의 기존 Core 경계를 그대로 사용합니다.

## 기능

- 실행 시작과 전체 context 취소
- 저장된 실행 이력 조회와 이벤트 재생
- Planning → Coding → Preflight → Testing → Reviewing → Fixing → Verifying 그래프
- 병렬 Reviewer 카드
- 역할·모델·Gateway route node Inspector
- 최근 4,096개 Live Events
- AI Gateway health, active/limit, 모델·역할
- Gateway drain/resume
- 도구 승인: 거부, 이번만 허용, 항상 허용
- 시스템 light/dark theme
- TalkBack용 상태 label과 48dp 이상 터치 목표

부분 node resume, 실행 중 그래프 편집, 기기 내 Ollama 실행은 지원하지 않습니다.

## Controller 준비

### 1. 토큰 생성

24자 이상 임의 token을 환경 변수로 설정합니다. 실제 운영에서는 충분히 긴 random token을 사용하세요.

```bash
export OLLI_CONTROLLER_TOKEN="$(openssl rand -hex 32)"
```

토큰을 `config.json`, 셸 history, 앱 소스에 넣지 마세요. 서비스 관리자 secret 환경으로 전달하는 것을 권장합니다.

### 2. Controller 빌드

```bash
go build -trimpath -o bin/olli-controller ./cmd/olli-controller
```

또는 전체 Go 검증:

```bash
scripts/safe-test ./controller ./cmd/olli-controller ./runstate
scripts/safe-exec __GO__ vet ./...
```

### 3. 로컬 전용 실행

기본 bind는 loopback입니다.

```bash
./bin/olli-controller \
  --workspace "$PWD" \
  --bind 127.0.0.1:8766
```

loopback 실행은 로컬 reverse proxy 뒤에 둘 때 사용합니다.

### 4. 사설망 원격 bind

Tailscale/WireGuard 주소에 bind하고 원격 bind를 명시적으로 허용합니다.

```bash
./bin/olli-controller \
  --workspace "$PWD" \
  --bind 100.64.0.10:8766 \
  --allow-remote-bind \
  --token-env OLLI_CONTROLLER_TOKEN
```

원격 bind는 token이 없으면 시작을 거부합니다. Controller의 내장 HTTP 서버는 TLS를 제공하지 않으므로 실제 모바일 연결에는 다음 중 하나를 사용하세요.

- Tailscale Serve의 HTTPS
- Caddy/Nginx의 HTTPS reverse proxy
- mTLS를 적용한 내부 reverse proxy

Ollama와 Controller 포트를 공용 인터넷에 직접 노출하지 마세요.

### API 보안 특성

- 기본 loopback bind
- 원격 bind opt-in
- remote bind 시 Bearer token 필수
- token은 constant-time 비교
- redirect 없는 클라이언트 사용 권장
- 64KB JSON body 상한
- unknown field·trailing JSON 거부
- `Cache-Control: no-store`
- `X-Content-Type-Options: nosniff`
- allowlist 기반 Origin 처리
- prompt와 source 전문을 Runtime Event metadata에 저장하지 않음

## Android 빌드

필수 환경:

- JDK 17
- Android SDK Platform 36
- Android Build Tools 35 이상

```bash
export JAVA_HOME=/path/to/jdk-17
export ANDROID_HOME=/path/to/android-sdk
./scripts/build-android-app build
```

또는 Android 프로젝트 안에서 직접 실행합니다.

```bash
cd apps/android
./gradlew testDebugUnitTest assembleDebug
```

APK:

```text
apps/android/app/build/outputs/apk/debug/app-debug.apk
```

기기에 설치:

```bash
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

## 앱 연결

1. 앱을 열고 Controller HTTPS URL을 입력합니다.
2. Bearer token을 입력합니다.
3. **연결**을 누릅니다.
4. 연결 성공 후 실행 이력, 그래프, 이벤트, Gateway 탭이 활성화됩니다.

URL 예:

```text
https://olli-controller.example.internal
```

배포 빌드는 HTTPS만 허용합니다. Debug 빌드는 Android emulator에서 다음 주소만 평문 HTTP로 허용합니다.

```text
http://10.0.2.2:8766
http://localhost:8766
```

LAN IP에 대한 평문 HTTP는 debug에서도 거부합니다.

### Token 저장 정책

- Controller URL: 앱 private preferences에 저장
- Bearer token: 앱 프로세스 메모리에만 유지
- 앱 재시작: token 재입력 필요
- Android backup: 비활성화

편의성보다 유출 시 영향이 큰 Controller 권한 token 보호를 우선합니다.

## 화면 사용법

### 실행

작업 목표를 입력해 실행합니다. 하나의 Controller는 한 번에 하나의 main-agent run을 허용하며 내부 Reviewer와 Detail Planner 병렬성은 유지됩니다.

실행 중에는 **전체 실행 취소**가 보입니다. 이는 Core context를 취소하며 이미 끝난 tool mutation을 되돌리는 기능은 아닙니다.

### 그래프

가로 스크롤 가능한 phase 그래프와 Reviewer fan-out을 표시합니다. 노드를 선택하면 역할, 모델, Gateway route node, status와 최신 메시지를 확인합니다.

### 이벤트

sequence 역순으로 이벤트를 표시합니다. Event Store polling은 마지막 sequence 이후만 요청하므로 연결이 잠시 끊겨도 재연결 후 누락 이벤트를 받을 수 있습니다.

### Gateway

노드별 active/limit을 progress indicator로 표시합니다.

- Drain: 새 lease 중단
- Resume: 새 lease 허용

진행 중 lease를 강제 종료하지 않습니다.

### Permission

pending permission은 1초 polling으로 앱에 나타납니다.

- 거부
- 이번만 허용
- 항상 허용

항상 허용은 Controller의 기존 config whitelist를 변경합니다. Android 앱 자체에는 workspace 권한이 없습니다.

## 에뮬레이터 테스트

Controller를 로컬에서 실행:

```bash
export OLLI_CONTROLLER_TOKEN="$(openssl rand -hex 32)"
go run ./cmd/olli-controller \
  --workspace "$PWD" \
  --bind 127.0.0.1:8766
```

Android emulator 앱에는 다음을 입력합니다.

```text
URL: http://10.0.2.2:8766
Token: OLLI_CONTROLLER_TOKEN 값
```

연결 화면만 검증하려면 Controller를 실행하지 않아도 APK 설치와 UI 렌더링을 확인할 수 있습니다.

```bash
adb install -r apps/android/app/build/outputs/apk/debug/app-debug.apk
adb shell monkey -p dev.olli.mobile.debug -c android.intent.category.LAUNCHER 1
```

## 수동 테스트 체크리스트

- [ ] HTTPS Controller 연결 성공
- [ ] 잘못된 token에서 401 표시
- [ ] 앱 재시작 후 URL은 남고 token은 비워짐
- [ ] harmless read-only 실행 시작
- [ ] Graph phase와 Live Events 갱신
- [ ] Reviewer fan-out 표시
- [ ] Gateway active/limit 갱신
- [ ] drain 후 신규 lease가 해당 노드를 피함
- [ ] permission 거부 시 도구가 실행되지 않음
- [ ] harmless permission 1회 승인 성공
- [ ] 실행 취소 후 cancelled 이벤트 확인
- [ ] 앱 재연결 후 실행 이력 재생
- [ ] light/dark mode 확인
- [ ] TalkBack에서 탭·노드·상태 읽기
- [ ] 회전 및 대형 화면에서 가로 그래프 스크롤 확인

## 검증 명령

```bash
scripts/safe-test ./...
scripts/safe-exec __GO__ vet ./...
scripts/safe-exec __GO__ test -race ./controller ./runstate ./gateway ./subagent ./agent

cd apps/android
JAVA_HOME=/path/to/jdk-17 \
ANDROID_HOME=/path/to/android-sdk \
./gradlew testDebugUnitTest assembleDebug
```

## 알려진 제한

- 이벤트 전달은 현재 1초 polling이며 push/WebSocket은 후속 단계입니다.
- Android 앱은 Controller certificate pinning을 아직 제공하지 않습니다. 사설 PKI 또는 신뢰 가능한 HTTPS reverse proxy를 사용하세요.
- biometric으로 token을 보호하는 선택적 persistent login은 아직 없습니다.
- artifact 파일 다운로드·미리보기는 포함하지 않습니다.
- Play Store 서명·배포 파이프라인은 포함하지 않습니다.
