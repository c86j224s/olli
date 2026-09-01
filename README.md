# 🤖 O.L.L.I. (Ollama-based Local LLM Interface)

**O.L.L.I.**는 로컬 [Ollama](https://ollama.com) 모델과 연동하여 동작하는 **Go 언어 기반 자율 AI 에이전트 인터페이스**입니다.

---

## ✨ 핵심 기능 (Key Features)

- **🕵️ Dedicated Subagent Delegation**:
  - **`delegate_researcher`**: 웹 검색(`web_search`) 및 URL 읽기(`read_url_content`) 전담 **Web Researcher Subagent**
  - **`delegate_coder`**: 코드 탐색(`grep_search`), 파일 보기(`view_file`), 파일 편집(`edit_file`) 전담 **Coder Subagent**
  - **`delegate_documenter` / `delegate_presenter`**: Markdown/HTML 산출물 경로를 보고하며, Documenter는 `.md`, Presenter는 `.html` artifact가 실제로 존재해야 성공 처리
  - **📄 1-Turn JSONL File Handover**: 서브에이전트 작업 수행 과정 및 결과는 `./sessions/subagents/subagent_<id>.jsonl` 파일로 저장되며 메인 에이전트에는 핵심 보고서만 전달되어 토큰 및 대화 오염 완전 방지!
- **🛡️ 3-Mode Tool Permission**: `auto`, `ask`, `accept-edit` (동적 `config.json` 화이트리스트 및 `[a] Always` 영구 등록)
- **🔒 Multi-Layer Security Boundary**: 홈 디렉토리(`~`), 시스템 루트(`/`), 워크스페이스 외곽 이탈(`..`) 완전 방어
- **🎯 Goal Steering & Autonomous Loop**: 매 턴마다 활성 목표(Goal)를 지속 주입하여 Goal Drift 없이 임무 달성
- **💾 JSONL Session Persistence & RAG Search**: 대화 내역 자동 지속화, 세션 로드/리네임, 과거 로그 RAG 검색
- **🌐 Environment & Temporal Context**: 타임존 포함 실시간 시공간 맥락 및 디렉토리 위치 자동 인지
- **⌨️ `ergochat/readline` CJK UTF-8 REPL**: 한글 백스페이스 완벽 지원, 방향키 히스토리, Tab 커맨드 자동 완성
- **🌊 Unified Real-time Streaming**: Thinking(추론 과정) ➡️ Tool Call ➡️ Response 실시간 스트리밍

---

## 안전한 개발 및 테스트

이 저장소의 테스트와 에이전트 실행은 실제 체크아웃이나 사용자 홈에서 직접 수행하지 않습니다. macOS의 `sandbox-exec`로 감싼 일회용 체크아웃에서 실행하며, 실제 홈과 실제 저장소 접근 및 네트워크를 차단하고 쓰기는 실행별 임시 디렉터리로 제한합니다. 샌드박스를 준비할 수 없으면 안전하지 않은 대체 경로로 실행하지 않고 실패합니다.

```bash
make test          # 정적 안전 검사 후 샌드박스에서 go test ./...
make vet           # 샌드박스에서 go vet ./...
make build         # 샌드박스에서 테스트 및 컴파일 검증(호스트 바이너리 미생성)
make run           # 호스트 실행 거부 — 전용 일회용 VM에서만 실행
make test-safety   # 고위험 테스트 패턴 정적 검사
make sandbox-smoke # 실제 체크아웃·외부 읽기/쓰기·모든 네트워크 차단 확인
make macos-security-integration # 호스트 실행 거부 후 VM 전용 명령 안내
```

`go test`, `go vet`, `go run`, `./olli`를 호스트에서 직접 실행하지 마세요. 적대적 실행 테스트는 실제 홈 대신 `t.TempDir()`로 만든 가짜 홈과 미끼 파일만 사용해야 합니다. 방어 코드가 모두 실패하더라도 테스트 소유 임시 디렉터리 밖에는 피해가 없어야 합니다.

대화형 O.L.L.I. 실행은 로컬 Ollama·ComfyUI·ACE-Step 접근과 내부 명령 샌드박스를 함께 요구하므로, 테스트용 `sandbox-exec` 안에서 중첩 실행하지 않습니다. 호스트 디렉터리, 자격 증명, 소켓을 공유하지 않는 전용 일회용 VM에서만 실행하세요. 일반 테스트는 모든 네트워크를 차단하고, 실사용 VM은 VM 내부 로컬 백엔드와 웹 검색에 필요한 outbound 네트워크만 별도 정책으로 허용합니다.

---

## ComfyUI 이미지 생성

메인 에이전트 도구 `image_generate`는 로컬 ComfyUI API 워크플로를 실행하고 첫 출력 이미지를 워크스페이스 내부 `artifacts/images`에 저장합니다. 이 도구는 민감 도구로 분류되어 whitelist 등록 여부와 관계없이 항상 권한 확인이 필요합니다.

각 멀티미디어 설정의 `enabled`를 `false`로 두면 해당 도구는 시작 시 등록되지 않습니다. 변경 사항은 O.L.L.I.를 다시 시작한 뒤 적용됩니다. 기존 설정 파일처럼 `enabled`가 생략된 경우에는 호환성을 위해 활성화됩니다.

- prerequisite 설치 스크립트:

```bash
./scripts/install-prerequisites.sh
```

비대화형으로 한 번에 진행하려면:

```bash
./scripts/install-prerequisites.sh --yes
```

현재 상태만 확인하려면:

```bash
./scripts/install-prerequisites.sh --check-only
```

백엔드 실행/상태/종료:

```bash
./scripts/backends.sh start
./scripts/backends.sh status
./scripts/backends.sh logs comfyui -f
./scripts/backends.sh stop
```

- ComfyUI API 서버는 로컬 HTTP 엔드포인트만 허용됩니다: `http://127.0.0.1:8188`, `http://localhost:8188`, `http://[::1]:8188`
- ComfyUI에서 API-format workflow JSON을 저장한 뒤, 워크스페이스 내부 경로만 `config.json`에 등록합니다.
- 현재 `config.json`은 `workflows/comfyui/flux2-klein-4b-distilled.json`을 `default_workflow` alias로 등록합니다. 다른 workflow를 추가할 때도 실제 API-format 파일과 정확한 node/input mapping이 준비된 alias만 등록하세요.

예시:

```json
"image_generation": {
  "enabled": true,
  "comfyui": {
    "endpoint": "http://127.0.0.1:8188",
    "output_dir": "artifacts/images",
    "timeout_seconds": 300,
    "max_image_bytes": 67108864,
    "workflows": {
      "default": {
        "path": "workflows/comfyui/default.json",
        "prompt_node_id": "6",
        "prompt_input": "text",
        "negative_prompt_node_id": "7",
        "negative_prompt_input": "text",
        "width_node_id": "5",
        "width_input": "width",
        "height_node_id": "5",
        "height_input": "height",
        "steps_node_id": "3",
        "steps_input": "steps",
        "seed_node_id": "3",
        "seed_input": "seed"
      }
    }
  }
}
```

---

## 생성 이미지 시각 검사

메인 에이전트 도구 `inspect_image`는 `artifacts/images` 아래의 생성 이미지를 로컬 Ollama 비전 모델로 분석합니다. 이미지 생성과 검사는 분리되어 있으므로, 시각적 품질이나 프롬프트 준수를 확인해야 할 때 명시적으로 호출합니다.

- 기본 모델: `gemma4:12b`
- 입력 파일은 `artifacts/images` 아래의 일반 파일만 허용되며 symlink는 거부됩니다.
- 이미지는 크기와 형식을 검증하고 PNG로 정규화한 뒤 loopback Ollama API로만 전송됩니다.
- 결과는 설명, 질문에 대한 답변, 품질 문제, 불확실성, 신뢰도를 포함하는 JSON입니다.
- 결과는 비전 모델의 평가이며 ground truth가 아닙니다. 중요한 세부 정보는 별도로 확인해야 합니다.

설정 예시:

```json
"image_inspection": {
  "enabled": true,
  "ollama": {
    "endpoint": "http://127.0.0.1:11434",
    "model": "gemma4:12b",
    "timeout_seconds": 300,
    "max_image_bytes": 67108864
  }
}
```

호출 예시:

```json
{
  "path": "artifacts/images/generated.png",
  "question": "Does this image show a photorealistic snake with exactly four visible legs?"
}
```

---

## OAW 에이전트 워크플로 프로토콜

OAW(**O.L.L.I. Agent Workflow Protocol**) v0.1은 O.L.L.I.가 등록된 도구를 제한된 순서와 명시적 데이터 바인딩으로 실행하기 위한 선언형 JSON 규약입니다.

- 프로토콜 명세: `workflows/agent/OAW_PROTOCOL.md`
- JSON Schema: `workflows/agent/oaw.schema.json`
- 첫 예제: `workflows/agent/image-generate-verify.oaw.json`
- 표준 확장자: `*.oaw.json`

첫 예제는 다음 절차를 선언합니다.

```text
image_generate → inspect_image → 결과 반환
```

OAW v0.1 runner는 시작 시 workflow·event schema와 Registry tool schema를 검증·snapshot하고, `workflows/agent/*.oaw.json`을 기존 tool permission 경계 안에서 순차 실행합니다. 메인 에이전트는 `list_workflows`, `get_workflow`, `run_workflow` 도구를 사용할 수 있고, CLI에서는 다음 명령을 제공합니다.

```text
/workflow list
/workflow show image-generate-verify
/workflow validate image-generate-verify
/workflow run image-generate-verify {"prompt":"a red fox in snow","verification_question":"Does the image match the prompt?"}
```

`run`의 마지막 인자는 JSON object이며 공백과 중첩 값을 그대로 보존합니다. 각 tool attempt는 `auto`, `ask`, `accept-edit`, 민감 도구 분류와 whitelist를 포함한 기존 권한 정책을 다시 적용합니다. 실행 결과는 구조화된 status/output/failure와 finalized `sessions/workflows/<run_id>.jsonl` 로그 경로를 반환합니다. 로그에는 raw input·arguments·result·media를 기록하지 않습니다.

---

## ACE-Step 음악 생성

메인 에이전트 도구 `audio_generate`는 로컬 ACE-Step API 서버에 텍스트 기반 음악 생성 작업을 제출하고 첫 WAV 결과를 워크스페이스 내부 `artifacts/audio`에 저장합니다. 이 도구도 항상 권한 확인이 필요하며 기본 whitelist에 추가되지 않습니다.

설치 스크립트는 기본적으로 ACE-Step도 `.local/ace-step` 아래에 설치 대상으로 포함합니다. 건너뛰려면:

```bash
./scripts/install-prerequisites.sh --skip-ace-step
```

ACE-Step만 확인/실행하려면:

```bash
./scripts/backends.sh status ace-step
./scripts/backends.sh start ace-step
./scripts/backends.sh logs ace-step -f
```

- ACE-Step API 서버는 로컬 HTTP 엔드포인트만 허용됩니다: `http://127.0.0.1:8001`, `http://localhost:8001`, `http://[::1]:8001`
- 첫 버전은 `text2music`만 지원하고 서버에 `audio_format: "wav"`, `batch_size: 1`을 강제합니다.
- 다운로드한 오디오는 RIFF/WAVE 구조 검증 후 `fmt `와 `data` chunk만 남긴 WAV로 다시 저장합니다.
- 모델 선택, LM backend, CPU offload 등은 ACE-Step 서버 환경변수(`ACESTEP_CONFIG_PATH`, `ACESTEP_LM_MODEL_PATH`, `ACESTEP_LM_BACKEND` 등)로 조정하세요.

예시:

```json
"audio_generation": {
  "enabled": true,
  "ace_step": {
    "endpoint": "http://127.0.0.1:8001",
    "output_dir": "artifacts/audio",
    "timeout_seconds": 900,
    "max_audio_bytes": 209715200,
    "poll_interval_ms": 1000,
    "max_duration_seconds": 600
  }
}
```

---

## 🚀 빌드 및 실행

```bash
./build.sh
./bin/olli
```
