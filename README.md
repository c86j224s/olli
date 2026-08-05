# 🤖 O.L.L.I. (Ollama-based Local LLM Interface)

**O.L.L.I.**는 로컬 [Ollama](https://ollama.com) 모델과 연동하여 동작하는 **Go 언어 기반 자율 AI 에이전트 인터페이스**입니다.

---

## ✨ 핵심 기능 (Key Features)

- **🛡️ 3-Mode Tool Permission**: `auto`, `ask`, `accept-edit` (동적 `config.json` 화이트리스트 관리)
- **🔒 Multi-Layer Security Boundary**: 홈 디렉토리(`~`), 시스템 루트(`/`), 워크스페이스 외곽 이탈(`..`) 완전 방어
- **🎯 Goal Steering & Autonomous Loop**: 매 턴마다 활성 목표(Goal)를 지속 주입하여 Goal Drift 없이 임무 달성
- **💾 JSONL Session Persistence & RAG Search**: 대화 내역 자동 지속화, 이름 기반 세션 로드/리네임, 과거 로그 RAG 검색
- **🌐 Environment & Temporal Context**: 타임존 포함 실시간 시공간 맥락 및 디렉토리 위치 자동 인지
- **⌨️ `ergochat/readline` CJK UTF-8 REPL**: 한글 백스페이스 완벽 지원, 방향키 명령어 히스토리, Tab 자동 완성
- **🌊 Unified Real-time Streaming**: Thinking(추론 과정) ➡️ Tool Call ➡️ Response 실시간 스트리밍

---

## 🛠️ CLI 명령어 목록

| 명령어 | 설명 |
|---|---|
| `/mode <auto\|ask\|accept-edit>` | 툴 실행 권한 모드 변경 |
| `/config whitelist` | `config.json` 화이트리스트 툴 목록 조회 |
| `/config allow <tool>` | 특정 툴을 `config.json` 화이트리스트에 추가 (자동 허용) |
| `/config deny <tool>` | 특정 툴을 화이트리스트에서 제거 (승인 요구) |
| `/goal set <목표 내용>` | 자율 목표 설정 (Goal Steering) |
| `/goal clear` | 목표 해제 |
| `/session list` | 저장된 세션 파일 목록 확인 |
| `/session new [이름]` | 새 세션 생성 |
| `/session load <이름/ID>` | 기존 세션 불러오기 (이름/부분 검색 가능) |
| `/session rename <새이름>` | 현재 또는 지정 세션 이름 변경 |
| `/summary` | 현재 대화 메모리 요약본 확인 |
| `/summarize` | LLM 대화 자동 요약 트리거 |
| `/numctx [크기]` | 컨텍스트 윈도우 크기 변경 (기본값: 32768) |
| `/tools` | 등록된 툴 목록 조회 |
| `/models` | 로컬 Ollama 모델 목록 조회 |
| `/model <이름>` | 사용 모델 변경 |

---

## 🚀 빌드 및 실행

```bash
# 유닛 테스트 후 빌드 및 실행
./build.sh
./toy-agent

# 다중 플랫폼 크로스 컴파일 (macOS ARM/AMD, Linux AMD64, Windows AMD64)
make cross
```
