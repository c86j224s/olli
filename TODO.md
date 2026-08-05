# 📌 O.L.L.I. Roadmap & TODO Ideas

## 🚀 Upcoming Ideas & Planned Features

### 1. ⚙️ Declarative Workflow Orchestration Engine (선언적 워크플로우 오케스트레이션 엔진)
- **개념**: 메인 에이전트(Orchestrator)가 서브에이전트들을 순차적으로/조건별로 실행할 수 있도록 가이드해 주는 표준 작업 절차(SOP Playbook) 엔진.
- **주요 기능**:
  - `./workflows/` 디렉토리에 JSON/YAML 형식의 워크플로우 템플릿 정의 (예: `feature_dev.json`, `doc_and_presentation.json`).
  - `/workflow list`, `/workflow run <name>` CLI 서브 커맨드 추가.
  - 메인 에이전트 전용 툴 `get_workflow(name)` 등록 ➡️ 오케스트레이터가 워크플로우 단계를 조회하여 6대 서브에이전트(`Researcher` ➔ `Coder` ➔ `Tester` ➔ `Reviewer` ➔ `Documenter` ➔ `Presenter`)를 완벽한 순서로 지휘.

---

## 🎯 Completed Features (구현 완료 내역)

- [x] **O.L.L.I. 6대 서브에이전트 드림팀 구축**
  - 🔍 `delegate_researcher`: 웹 정보 조사
  - 💻 `delegate_coder`: 소프트웨어 코드 작성 및 파일 수정
  - 🧪 `delegate_tester`: `go test` 및 빌드 터미널 동적 실측
  - 🧐 `delegate_reviewer`: 코드 품질, 가독성, 엣지 케이스 정적 리뷰
  - 📝 `delegate_documenter`: 마크다운 기술 문서, README 작성
  - 📊 `delegate_presenter`: 글래스모피즘 인터랙티브 HTML PPT 슬라이드 생성
- [x] **단일 색상 톤 + 밝기/스타일 구분 체계 (Single-Hue Multi-Intensity Design)**
  - 메인 에이전트 및 서브에이전트별 시그니처 1색상 톤(Cyan, Amber, Magenta, Emerald, Blue, White) + 밝기(Bold)/스타일(Italic/Dim) 구분.
- [x] **동적 화이트리스트 설정 (`config.json`) 및 승인 옵션**
  - `accept-edit` 모드 지원 및 대화형 권한 창에서 `[a] Always` 화이트리스트 등록 지원.
- [x] **철통 보안 방어막 (`tools/security.go`)**
  - 워크스페이스 경계 탈출(`..`), 홈/루트 디렉토리 파괴(`rm -rf ~`, `rm -rf /`), 자기 삭제(`rm -rf .`) 무조건 차단.
- [x] **Ollama 10분 스트리밍 타임아웃 방어막**
  - 대용량 서브에이전트 코드 출력 시 `context deadline exceeded` 튕김 방지.
