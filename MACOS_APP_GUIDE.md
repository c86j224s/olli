# O.L.L.I. macOS Desktop 가이드

O.L.L.I. Desktop은 기존 Go Core와 동일한 Agent, Permission Engine, Development Graph, AI Gateway를 사용하는 네이티브 SwiftUI 관찰·제어 화면입니다. CLI를 WebView에 넣은 것이 아니라, Go Desktop Core가 구조화 이벤트를 내보내고 SwiftUI가 이를 시각화합니다.

## 현재 MVP 범위

- Development Graph 실시간 표시
- Planning → Coding → Preflight → Testing → Reviewing → Fixing → Verifying 상태
- 병렬 Reviewer fan-out 표시
- 서브에이전트 역할, 모델, Gateway 노드, 실행시간 Inspector
- 라이브 이벤트 타임라인
- AI Gateway 노드 health와 active/limit 확인
- Gateway drain/resume
- 작업 시작과 전체 실행 취소
- CLI와 동일한 도구 권한 승인·거부·항상 허용
- light/dark mode 자동 대응
- 키보드와 VoiceOver가 읽을 수 있는 상태 아이콘·레이블

Checkpoint 기반 부분 재실행, 실행 중 그래프 edge 수정, Coder worktree 병렬화는 아직 포함하지 않습니다. 해당 기능은 Core 상태 직렬화와 재개 계약이 준비된 뒤 추가해야 합니다.

## 구조

```text
SwiftUI macOS App
    │ JSON Lines over stdin/stdout
    ▼
cmd/olli-desktop-core
    ├── agent.Agent
    ├── Permission Engine
    ├── DevelopmentTeamRunner
    ├── AI Gateway
    └── runstate.MemoryStore
```

Desktop Core는 사용자 프롬프트나 소스 전문을 이벤트 metadata에 넣지 않습니다. 이벤트에는 실행 ID, 그래프·노드 ID, 역할, 모델, Gateway 노드 ID, 상태, 도구 이름과 제한된 메시지만 전달합니다. token, secret, authorization, header, source/content 계열 metadata key는 공통 Event Store에서 제거합니다.

## 빌드

필수 환경:

- macOS 14 이상
- Xcode 및 Swift 6 toolchain
- 프로젝트에서 요구하는 Go 버전
- 로컬 또는 AI Gateway Ollama 설정

앱 번들을 빌드합니다.

```bash
./scripts/build-macos-app build
```

결과:

```text
bin/macos/O.L.L.I. Desktop.app
```

빌드 스크립트는 다음 두 실행 파일을 앱 번들에 넣습니다.

```text
Contents/MacOS/OlliDesktop
Contents/MacOS/olli-desktop-core
```

정리:

```bash
./scripts/build-macos-app clean
```

현재 스크립트는 로컬 개발용 unsigned 앱을 만듭니다. 다른 사용자에게 배포하려면 Apple Developer ID 서명, hardened runtime, entitlements 검토, notarization을 별도 릴리스 단계로 추가해야 합니다.

## 실행

현재 저장소의 안전 정책상 Agent 실사용 실행은 호스트 체크아웃이 아니라 전용 개발·운영 환경에서 수행해야 합니다. 해당 환경에서 저장소 루트를 현재 디렉터리로 두고 앱을 엽니다.

```bash
open 'bin/macos/O.L.L.I. Desktop.app'
```

개발 중 Swift 실행 파일을 직접 띄울 때는 Core 위치를 지정할 수 있습니다.

```bash
export OLLI_DESKTOP_CORE="$PWD/bin/olli-desktop-core"
swift run --package-path apps/macos/OlliDesktop OlliDesktop
```

앱은 현재 디렉터리 또는 앱 위치의 상위 경로에서 `go.mod`와 `config.json`을 찾아 workspace root를 결정합니다. 찾지 못하면 실행을 거부합니다.

## 화면 사용법

### 작업 시작

상단 입력창에 목표를 입력하고 **실행**을 누릅니다. 실행 중에는 새 작업을 동시에 시작할 수 없고 **취소** 버튼으로 현재 context를 취소할 수 있습니다.

### 그래프 Canvas

노드 카드는 상태 색과 아이콘을 함께 사용합니다.

- 청록 파형: 실행 중
- 녹색 체크: 완료
- 주황 시계: 대기·drain
- 빨간 경고: 실패·unhealthy·circuit-open
- 회색 점: 아직 실행되지 않음

색만으로 상태를 구분하지 않으며 노드 카드와 VoiceOver label에 상태 텍스트를 함께 제공합니다.

### Node Inspector

그래프나 사이드바에서 노드를 선택하면 다음을 봅니다.

- 역할
- 모델
- Gateway route node ID
- 현재 phase와 status
- 경과 시간
- 최근 메시지
- 해당 노드의 이벤트 목록

### Live Events

최근 이벤트를 역순으로 표시합니다. 이벤트를 선택하면 해당 노드 Inspector로 이동합니다. UI는 최대 4,096개 이벤트를 유지하며 Core Store도 bounded history를 사용합니다.

### Gateway Inspector

노드별 active/limit을 단일 progress mark로 표시합니다. 이것은 비교 차트가 아니라 각 노드의 현재 점유율을 보여 주는 status meter입니다. 모델·역할은 색 대신 텍스트로 표시합니다.

- **Drain**: 진행 중 lease는 유지하고 새 lease 배정을 중단
- **Resume**: 새 lease 배정을 재개
- 새로 고침: health probe 후 상태 갱신

### 권한 승인

민감 도구가 요청되면 modal sheet가 나타납니다.

- 거부
- 이번만 허용
- 항상 허용

항상 허용은 CLI와 동일하게 `config.json` whitelist를 변경합니다. 앱이라고 해서 Permission Engine이나 sandbox 경계를 우회하지 않습니다.

## 이벤트 계약

주요 이벤트:

```text
run_started
phase_changed
node_started
model_routed
tool_started
tool_completed
node_completed
node_failed
assistant_content
run_completed
run_failed
run_cancelled
```

공통 필드:

```text
sequence, timestamp, run_id, kind,
graph_id, node_id, phase, role, model,
route_node_id, status, duration_ms
```

`runstate.MemoryStore`는 모든 publisher에 대해 단조 증가 sequence를 부여하고, snapshot과 subscription을 제공합니다. 구독자가 느릴 경우 Core 실행을 막지 않도록 bounded channel을 사용하며, UI는 snapshot을 다시 요청해 누락을 복구할 수 있습니다.

## 개발 검증

Go Core:

```bash
scripts/safe-test ./runstate ./subagent ./agent ./gateway ./cmd/olli-desktop-core
scripts/safe-exec __GO__ vet ./...
scripts/safe-exec __GO__ test -race ./runstate ./gateway ./subagent ./agent
```

SwiftUI:

```bash
swift build \
  --package-path apps/macos/OlliDesktop \
  --scratch-path "$PWD/.desktop-swift-build"
```

패키징:

```bash
bash -n scripts/build-macos-app
scripts/build-macos-app build
```

`.desktop-swift-build`처럼 개발 중 생성한 build directory는 커밋하지 마세요. 기본 패키징 산출물은 이미 ignore된 `bin/` 아래에 생성됩니다.

## 알려진 제한

- 앱 종료 후 이벤트 이력의 전용 run log 재생은 다음 단계입니다. 현재 session JSONL과 workflow log는 기존 방식으로 남지만 Desktop Event Store 자체는 메모리 기반입니다.
- 개별 graph node 재시작·resume은 지원하지 않습니다.
- 실행 중 모델 변경은 지원하지 않습니다.
- Gateway endpoint 추가·삭제는 `config.json` 변경 후 재시작이 필요합니다.
- unsigned 개발 앱이므로 Gatekeeper 배포 절차는 포함하지 않습니다.
- 현재는 한 번에 하나의 main-agent run만 허용합니다. 내부 읽기 전용 Reviewer와 Detail Planner 병렬성은 유지됩니다.

## 다음 단계

1. Desktop 이벤트의 안전한 JSONL 영속화와 실행 이력 재생
2. graph checkpoint와 읽기 전용 node 재실행
3. 실행 전 Architecture Plan 승인 화면
4. artifact 미리보기와 Presenter deck 열기
5. Developer ID 서명·notarization 릴리스 파이프라인
6. 독립 worktree 기반 Coder 병렬화
