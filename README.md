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

## ComfyUI 이미지 생성

메인 에이전트 도구 `image_generate`는 로컬 ComfyUI API 워크플로를 실행하고 첫 출력 이미지를 워크스페이스 내부 `artifacts/images`에 저장합니다. 이 도구는 항상 권한 확인이 필요하며 기본 whitelist에 추가되지 않습니다.

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
- 기본 `config.json`은 workflow alias를 비워 둡니다. 실제 파일을 추가한 경우에만 `workflows`에 alias를 등록하세요.

예시:

```json
"image_generation": {
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
