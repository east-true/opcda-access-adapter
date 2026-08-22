# OPC DA Access Adapter — 전체 설계 문서

> **문서 상태:** Implementation Baseline Draft  
> **기준일:** 2026-08-22  
> **대상 독자:** 프로젝트를 처음 접하는 개발자, 리뷰어, 기여자, 운영자  
> **작업명:** OPC DA Access Adapter  
> **구현 언어:** Go  
> **배포 대상:** Windows  
> **초기 검증 Frontend:** HTTP/JSON  
> **향후 승인된 Frontend 방향:** gRPC, OPC UA
>
> 이 문서는 프로젝트의 목적, 설계 철학, 경계, 런타임 구조, 데이터 계약, 초기 HTTP API, 향후 Frontend 확장 규칙, COM 제약, 오류/재연결/보안/테스트/호환성 정책을 하나의 기준 문서로 정의한다.  
> 별도 ADR 또는 명시적인 설계 변경 없이 이 문서의 **불변 조건(Invariants)** 을 깨는 구현은 허용하지 않는다.

---

## 1. 문서 목적

이 프로젝트는 단순히 “OPC DA 값을 HTTP로 읽는 프로그램”이나 “DA를 UA로 변환하는 Gateway”를 만드는 것이 아니다.

목표는 다음 한 문장으로 정의한다.

> **OPC DA Server의 원래 데이터와 동작 의미를 바꾸지 않으면서, 현대 애플리케이션이 COM/DCOM 또는 OPC DA 자체를 직접 알 필요 없이 접근할 수 있게 하는 얇은 Access Adapter를 만든다.**

따라서 이 문서는 기능 목록만 설명하지 않는다. 오히려 다음을 더 중요하게 다룬다.

1. 프로젝트가 왜 존재하는지
2. 무엇을 반드시 보존해야 하는지
3. 무엇을 절대로 하지 않는지
4. 어떤 확장이 허용되고 어떤 확장이 금지되는지
5. OPC DA/COM이라는 레거시 기술의 제약을 Go 런타임 안에서 어떻게 격리할지
6. Frontend가 늘어나더라도 왜 Gateway/Integration Platform으로 변질되지 않아야 하는지
7. 초기 HTTP 구현이 최종 제품의 중심이 아니라 **DA Runtime 검증 도구이자 첫 Access Frontend**라는 점
8. 향후 gRPC와 OPC UA가 추가될 때도 동일한 DA-native contract를 공유해야 한다는 점

이 문서를 읽은 사람은 과거 대화나 설계 배경을 알지 못해도 구현 방향과 금지선을 이해할 수 있어야 한다.

---

# 2. 배경과 문제 정의

## 2.1 현실적인 문제

산업 현장에는 여전히 OPC DA 기반 시스템이 존재한다.

기존 설비나 소프트웨어가 안정적으로 동작하고 있다면, 단순히 프로토콜이 오래되었다는 이유만으로 전체 시스템을 OPC UA로 교체하는 것은 현실적으로 쉽지 않다. 교체에는 다음과 같은 비용이 발생할 수 있다.

- 기존 DA Server 교체 또는 업그레이드
- 벤더 라이선스 비용
- 설비 정지와 검증 비용
- 운영자/개발자 학습 비용
- 기존 시스템과 신규 시스템의 동시 운영
- 새로운 Client/Server 조합의 호환성 검증

반면 새로운 애플리케이션은 OPC DA의 COM/DCOM 환경을 직접 다루고 싶어 하지 않거나, 아예 다룰 수 없는 경우가 많다.

예:

- Go/Java/Python 기반 Backend 서비스
- 컨테이너 또는 별도 서비스 계층
- OPC UA Client만 지원하는 신규 프로그램
- HTTP API만 사용하고 싶은 내부 도구
- typed RPC를 원하는 서비스
- DCOM 설정과 COM threading을 애플리케이션 코드에 끌어들이고 싶지 않은 개발자

이 프로젝트는 **기존 DA Server를 교체하지 않고**, 그 앞에 작고 명확한 compatibility layer를 두는 문제를 푼다.

---

## 2.2 처음 검토했던 DA→UA 전용 방향

초기 아이디어는 다음과 같았다.

```text
Existing OPC DA Server
        |
        | COM / OPC DA
        v
   DA -> UA Adapter
        |
        | OPC UA
        v
Existing OPC UA Client
```

이 방향은 명확하지만, 조사 결과 이미 여러 상용/오픈소스 DA→UA Wrapper/Gateway가 존재한다.

따라서 단순히 다음을 만드는 것은 프로젝트의 존재 이유로 충분하지 않다고 판단했다.

> “Go로 만든 또 하나의 OPC DA→UA 변환기”

특히 기존 구현과 비교했을 때 언어 선택이나 라이브러리 선택만으로는 제품 가치가 충분히 차별화되지 않는다.

---

## 2.3 Access Adapter로 확장한 이유

최종적으로 문제 정의를 한 단계 아래로 내렸다.

**UA가 목적이 아니라 OPC DA 자체에 대한 현대적인 접근이 목적**이다.

```text
                    OPC DA Server
                          |
                          v
                  +----------------+
                  | OPC DA Runtime |
                  +--------+-------+
                           |
                    DA-native contract
                           |
              +------------+------------+
              |            |            |
              v            v            v
          HTTP/JSON       gRPC        OPC UA
```

중요한 점은 **Source는 늘어나지 않는다.**

확장되는 것은 오직 접근 방법(Access Frontend)이다.

---

# 3. 제품 정의

## 3.1 공식 제품 정의

> **OPC DA Access Adapter는 하나의 OPC DA Server를 source로 사용하고, OPC DA의 Browse/Read/Write/(향후 Subscribe) capability와 source semantics를 변경하지 않은 채 현대적인 Access Frontend를 통해 노출하는 얇은 compatibility adapter다.**

---

## 3.2 이 프로젝트가 아닌 것

이 프로젝트는 다음 제품이 아니다.

- 산업용 Integration Gateway
- Protocol Converter Platform
- IoT Gateway
- Historian
- SCADA
- OPC UA SDK
- OPC DA SDK
- Message Broker
- Asset Modeling Platform
- ETL/Transformation Engine
- Data Normalization Service
- Rule Engine
- Multi-source Aggregator

이 구분은 이름의 문제가 아니라 아키텍처의 문제다.

---

# 4. 최상위 불변 조건

아래 규칙은 프로젝트의 가장 중요한 제약이다.

## INV-1. Source Protocol은 OPC DA 하나뿐이다

허용:

```text
OPC DA -> Adapter -> Frontend
```

금지:

```text
Modbus -----+
S7 ---------+
BACnet -----+--> Common Core --> Frontends
OPC DA -----+
```

다음 source protocol 추가는 이 프로젝트의 범위를 깨므로 허용하지 않는다.

- Modbus
- Siemens S7
- BACnet
- EtherNet/IP
- MQTT source
- OPC UA source
- REST source
- Database source
- 기타 산업 프로토콜

다른 source가 필요하다면 별도 프로젝트로 다룬다.

---

## INV-2. Source semantics를 재해석하지 않는다

Adapter는 DA Server가 제공한 의미를 바꾸지 않는다.

금지 예:

- 단위 변환
- scaling
- offset
- tag rename
- tag merge
- tag split
- derived value
- 계산식
- smoothing
- interpolation
- “last good value”를 정상값처럼 반환
- 잘못된 값을 임의 수정
- timestamp 재생성 후 source timestamp처럼 표시
- Bad Quality를 Good으로 변경
- vendor-specific 의미 추론
- Asset/Device 구조 추론

허용되는 것은 **target protocol이 요구하는 결정적 representation mapping**뿐이다.

---

## INV-3. Source가 제공한 정보는 가능한 한 버리지 않는다

“얇은 Adapter”라는 이유로 source metadata를 의도적으로 삭제해서는 안 된다.

최소 보존 대상:

- ItemID
- Canonical VARTYPE / DataType
- Value
- Quality
- Timestamp
- Timestamp presence
- HRESULT
- Access Rights
- source가 직접 제공한 관련 item property

단, target frontend가 구조적으로 모든 정보를 표현할 수 없는 경우 손실을 명시적으로 문서화한다.

---

## INV-4. 새로운 공통 산업 데이터 모델을 만들지 않는다

다음과 같은 공통 모델을 만들지 않는다.

```text
Asset
Device
Metric
Measurement
Telemetry
NormalizedTag
Point
Signal
CommonValue
```

Core model은 반드시 DA-native vocabulary를 유지한다.

예:

```text
DAItem
DAValue
DAQuality
DAError
DAAccessRights
DABrowseEntry
```

---

## INV-5. Frontend는 DA capability를 노출하기만 한다

허용되는 Frontend operation의 중심은 다음이다.

```text
Browse
Read
Write
Subscribe
```

새 Frontend가 다음 개념을 요구한다면 해당 Frontend는 원칙적으로 이 프로젝트에 들어올 수 없다.

- Topic routing
- Asset query
- Rule execution
- Storage query
- Aggregation query
- Transformation pipeline
- Data enrichment
- Schema registry
- Workflow
- Event processing DSL

---

## INV-6. 영속 Process Data 저장소를 만들지 않는다

금지:

- Historian
- 시계열 DB
- last-good-value DB
- tag value cache DB
- event history DB
- persistent telemetry store

허용:

- 설정 파일
- 로그
- 프로세스 내부의 bounded runtime state
- COM handle cache
- 일시적인 browse/request state

운영 로그에도 process value를 기본적으로 기록하지 않는다.

---

## INV-7. 하나의 Adapter instance는 하나의 OPC DA Server를 담당한다

초기/기본 아키텍처는 다음과 같다.

```text
Adapter Process A -> DA Server A
Adapter Process B -> DA Server B
```

하나의 process가 여러 DA Server를 집계해서 하나의 namespace로 노출하는 구조는 만들지 않는다.

이 제약의 이유:

- source routing이 생기는 것을 방지
- namespace collision 방지
- multi-server aggregation Gateway로 변질되는 것을 방지
- 장애 경계 단순화
- 호환성 문제를 server 단위로 격리
- config를 단순화

여러 DA Server가 필요하면 Adapter instance를 여러 개 실행한다.

---

## INV-8. Remote DCOM은 이 프로젝트의 책임이 아니다

초기 지원 대상은 **Adapter가 실행되는 Windows 머신에서 local COM으로 접근 가능한 OPC DA Server**다.

```text
[Windows Machine]
  OPC DA Server
       ^
       | Local COM
       |
  Access Adapter
```

원격 DCOM 설정/터널링은 별도의 문제다.

Remote OPC DA가 필요하면 별도 **DA Tunneler / DCOM-less Proxy** 프로젝트에서 해결한다.

Access Adapter에 tunneling 기능을 합치지 않는다.

---

## INV-9. correctness가 concurrency보다 우선한다

Go가 goroutine을 쉽게 제공한다는 이유로 COM object를 여러 goroutine에서 직접 호출하지 않는다.

초기 구현은 COM access를 전용 OS thread에 직렬화한다.

성능 최적화는 실제 benchmark와 vendor compatibility 테스트가 확보된 이후에만 한다.

---

## INV-10. Unsupported는 명시적으로 실패한다

지원하지 않는 DA VARTYPE, Browse capability, Write 형태 등을 발견했을 때 임의 변환으로 “동작하는 것처럼” 만들지 않는다.

예:

```text
VT_UNKNOWN -> string 변환   X
SAFEARRAY -> flat JSON      X
Bad Quality -> last good    X
unknown HRESULT -> success  X
```

대신:

```text
explicit unsupported/error result
+
raw source code/type information where available
```

를 반환한다.

---

# 5. 기술 스택과 구현 제약

## 5.1 언어

**Go**를 사용한다.

선택 이유는 단순한 선호가 아니라 다음 목표와 맞기 때문이다.

- 단일 binary 배포
- Windows API 직접 호출 가능
- 작은 runtime footprint
- 네트워크 Frontend 구현 용이
- goroutine과 COM runtime을 명확히 분리 가능
- x86 / x64 binary 빌드 가능
- 장시간 daemon/service 형태 운영에 적합

---

## 5.2 OPC DA/OPC UA 라이브러리 의존 정책

이 프로젝트는 기존 OPC DA 또는 OPC UA 구현 라이브러리를 핵심 implementation dependency로 사용하지 않는다.

예를 들어 다음 프로젝트들은 참고/interop 대상일 수 있으나 구현 기반으로 채택하지 않는다.

- konimarti/opc
- huskar-t/opcda
- OpenOPC/OpenOPC-DA
- TitaniumAS
- asyncua
- open62541
- 기타 범용 OPC DA/UA SDK

이 제약의 목적은 “모든 것을 직접 만들기” 자체가 아니다.

목적은 **필요한 vertical slice만 구현하는 작은 Adapter**를 유지하는 것이다.

허용되는 일반 목적 dependency의 예:

- Windows syscall/low-level helper
- HTTP 관련 일반 라이브러리
- 향후 공식/일반 gRPC runtime
- test library

단, dependency 도입은 supply-chain, license, injection surface, 유지보수성을 검토해야 한다.

---

## 5.3 UA는 범용 SDK로 구현하지 않는다

향후 OPC UA Frontend를 직접 구현할 경우 `internal/opcua`를 범용 UA SDK로 확장하지 않는다.

필요한 기능만 구현한다.

예:

```text
UA-TCP framing
UA Binary
SecureChannel minimum
Session
Browse
Read
Write
향후 Subscription/MonitoredItem/Publish
```

Events, Historical Access, PubSub 등은 DA Access Adapter가 필요로 하지 않는 한 구현하지 않는다.

---

# 6. 운영 플랫폼

## 6.1 Windows 전용

OPC DA는 COM/DCOM 기반이므로 DA Runtime은 Windows에서 동작한다.

초기 지원 build target:

```text
windows/386
windows/amd64
```

---

## 6.2 Bitness

32-bit와 64-bit 환경을 별도로 검증한다.

일부 DA Server/COM registration은 bitness에 영향을 받을 수 있으므로 다음을 보장한다고 가정해서는 안 된다.

> “64-bit binary 하나가 모든 DA Server에 접속한다.”

Compatibility Matrix에서 다음을 기록한다.

- Windows version
- Adapter architecture
- DA Server vendor/product/version
- DA Server bitness 또는 COM registration 특성
- Connect
- Browse
- Read
- Write
- Subscribe(구현 후)

---

# 7. 전체 아키텍처

```text
                           +-----------------------+
                           |   OPC DA Server       |
                           |  Existing / Legacy    |
                           +-----------+-----------+
                                       ^
                                       |
                                  Local COM
                                       |
                           +-----------+-----------+
                           |                       |
                           |   OPC DA Runtime      |
                           |                       |
                           | - COM Apartment       |
                           | - Connection          |
                           | - Browse              |
                           | - Group/Item handles  |
                           | - Read                |
                           | - Write               |
                           | - Reconnect           |
                           | - Subscribe (future)  |
                           |                       |
                           +-----------+-----------+
                                       |
                              DA-native Contract
                                       |
                +----------------------+----------------------+
                |                      |                      |
                v                      v                      v
        +---------------+      +---------------+      +---------------+
        | HTTP/JSON     |      | gRPC          |      | OPC UA        |
        | first         |      | future        |      | future        |
        +---------------+      +---------------+      +---------------+
```

핵심 의존 방향:

```text
Frontend -> DA Runtime
```

금지:

```text
DA Runtime -> HTTP
DA Runtime -> gRPC
DA Runtime -> UA
```

DA Runtime은 어떤 Frontend가 자신을 호출하는지 몰라야 한다.

---

# 8. 프로세스 내부 구조

권장 package 구조:

```text
cmd/
  adapter/
    main.go

internal/
  opcda/
    runtime_windows.go
    com_windows.go
    server_windows.go
    group_windows.go
    item_windows.go
    browse_windows.go
    read_windows.go
    write_windows.go
    variant_windows.go
    quality.go
    hresult.go

  domain/
    item.go
    value.go
    error.go
    operation.go

  frontend/
    http/
      server.go
      browse.go
      read.go
      write.go
      encoding.go

  app/
    service.go
    config.go
    lifecycle.go
```

향후:

```text
internal/frontend/grpc/
internal/frontend/opcua/
```

중요한 것은 폴더명 자체가 아니라 의존 방향이다.

---

# 9. OPC DA Runtime

## 9.1 역할

DA Runtime은 프로젝트의 핵심이다.

책임:

- COM 초기화/정리
- DA Server activation
- server connection lifecycle
- group lifecycle
- item registration
- Browse
- ReadBatch
- WriteBatch
- source metadata 획득
- HRESULT 보존
- reconnect
- handle invalidation
- future subscription callbacks

책임이 아닌 것:

- HTTP JSON encoding
- gRPC protobuf
- UA StatusCode
- 인증 정책
- UI
- DB
- data normalization

---

# 10. COM Threading 모델

## 10.1 핵심 규칙

COM을 사용하는 thread는 COM library를 초기화해야 한다.

Go runtime은 goroutine을 OS thread 사이에서 이동시킬 수 있으므로 COM object를 일반 goroutine에서 직접 유지하면 안 된다.

초기 구조:

```text
HTTP/gRPC/UA goroutines
        |
        | request
        v
+-------------------------+
| DA command queue        |
+------------+------------+
             |
             v
+-------------------------+
| Dedicated DA OS Thread  |
| runtime.LockOSThread()  |
| CoInitializeEx(...)     |
| COM objects owned here  |
+------------+------------+
             |
             v
        OPC DA Server
```

---

## 10.2 Apartment

초기 구현은 compatibility 우선으로 전용 apartment-threaded runtime을 기본 설계로 삼는다.

전용 thread에서:

1. `runtime.LockOSThread()`
2. `CoInitializeEx`
3. COM object 생성
4. DA command 처리
5. 필요한 message pumping
6. COM object Release
7. `CoUninitialize`
8. thread 종료

를 수행한다.

COM interface pointer를 다른 goroutine/thread에 직접 전달하지 않는다.

향후 MTA 또는 복수 worker 구조를 검토할 수 있지만, 실제 vendor compatibility 근거 없이 변경하지 않는다.

---

## 10.3 Message Pump

STA를 사용할 경우 message loop requirement를 무시하지 않는다.

특히 향후 다음 callback이 들어오면 message dispatch가 필수적이다.

- `IOPCDataCallback`
- `IOPCShutdown`
- connection point callback

v0가 sync Read/Write만 사용하더라도, 설계상 STA message processing을 안전하게 수용할 수 있어야 한다.

---

# 11. OPC DA Baseline

초기 목표는 **OPC DA 2.05a**다.

주요 interface:

- `IOPCServer`
- `IOPCItemMgt`
- `IOPCSyncIO`
- `IOPCBrowseServerAddressSpace` — optional
- `IOPCItemProperties`
- `IOPCCommon`
- `IConnectionPointContainer`
- 향후 `IOPCDataCallback` 관련 connection point
- 필요 시 group state 관련 interface

OPC DA 3.0 지원은 v0 필수가 아니다.

---

# 12. Runtime은 Stateless가 아니다

프로젝트는 process data를 저장하지 않지만 runtime protocol state는 존재한다.

정확한 표현:

> **No persistent process-data storage, but stateful protocol runtime.**

runtime state 예:

- COM initialized state
- DA server COM interface
- Group server handle
- Group object/interface
- item server handles
- item registration cache
- canonical VARTYPE
- access rights
- Browse current position during one operation
- reconnect state
- generation/incarnation
- future callback registrations

이 state를 “없다”고 가정하면 DA 2.05a 구현 자체가 틀어진다.

---

# 13. Server Connection Lifecycle

상태 예:

```text
STARTING
   |
   v
CONNECTING
   |
   +------ success ------> CONNECTED
   |                         |
   |                         |
   +------ failure ----------+
                             |
                             v
                        RECONNECTING
                             |
                             v
                         CONNECTED

shutdown -> STOPPING -> STOPPED
```

추가로 심각한 COM hang 또는 내부 불변식 위반 시:

```text
DEGRADED
```

상태를 둘 수 있다.

---

## 13.1 연결 설정

v0 source identifier:

- ProgID
- 또는 CLSID

둘 중 하나를 명시한다.

Remote host는 받지 않는다.

예:

```text
source.progId = "Matrikon.OPC.Simulation.1"
```

---

## 13.2 재연결

연결이 끊긴 경우:

1. 현재 연결을 `disconnected`로 표시
2. 기존 group/item handle을 모두 무효화
3. stale handle을 재사용하지 않음
4. background reconnect 시도
5. 새 connection generation 생성
6. group을 다시 생성
7. item handle은 필요 시 lazy re-register
8. 성공 후 `connected` 상태로 복귀

재연결 중 이전 값을 “Good”으로 반환하지 않는다.

---

## 13.3 Backoff

연결 실패 시 busy-loop 하지 않는다.

reconnect는 bounded exponential backoff + jitter 구조를 사용할 수 있다.

정확한 기본 간격은 구현 전 config default로 결정하되 다음 원칙을 지킨다.

- 무한 tight retry 금지
- 로그 폭주 금지
- server 복구 후 자동 회복 가능
- 운영자가 현재 상태를 확인 가능

---

# 14. Group / Item Handle 관리

## 14.1 왜 필요하며 왜 제거할 수 없는가

DA 2.05a Read/Write는 일반적으로 다음 흐름을 사용한다.

```text
IOPCServer::AddGroup
        |
        v
IOPCItemMgt::AddItems
        |
        v
IOPCSyncIO::Read / Write
```

따라서 HTTP 요청 하나마다 “ItemID 문자열만 넘겨서 stateless read”하는 구조로 생각하면 안 된다.

---

## 14.2 내부 Group

v0는 하나의 Adapter instance가 하나의 DA Server를 담당하므로, 단순한 내부 Group을 유지한다.

기본 전략:

- 연결 시 internal Group 생성
- Item은 최초 사용 시 lazy AddItems
- server handle cache
- reconnect 시 전부 폐기

---

## 14.3 Item registration cache

cache는 영속 데이터가 아니라 runtime optimization이다.

보존 정보 예:

```text
ItemID
ServerHandle
CanonicalVARTYPE
AccessRights
ConnectionGeneration
```

규칙:

- 반드시 bounded 해야 한다.
- reconnect generation이 바뀌면 old handle 금지
- invalid handle/unknown item 오류 시 cache entry 재검증 가능
- cache 때문에 source 오류를 숨기면 안 됨
- cache persistence 금지

정확한 최대 item 수는 구현 benchmark 후 설정한다.

---

# 15. Browse

## 15.1 Browse는 optional capability다

DA 2.x의 `IOPCBrowseServerAddressSpace`는 optional이므로 모든 OPC DA 2.05a Server가 Browse를 지원한다고 가정하지 않는다.

따라서:

```text
Browse unsupported != Adapter unusable
```

알고 있는 ItemID에 대해서는 Read/Write가 가능할 수 있다.

---

## 15.2 Browse unsupported일 때 금지되는 fallback

Browse를 지원하지 않는 서버를 위해 다음을 만들지 않는다.

- CSV tag database
- manual tag mapping DB
- persisted tag registry
- inferred hierarchy
- delimiter 기반 hierarchy 강제 생성

대신 capability를 명시한다.

```text
browse: unsupported
read: available
write: source-dependent
```

사용자는 알고 있는 ItemID로 Read/Write할 수 있다.

---

## 15.3 Browse는 source를 직접 본다

기본 철학:

> Browse 결과를 Adapter가 별도 모델로 유지하지 않는다.

금지:

```text
startup full scan -> DB -> Browse from DB
```

기본:

```text
Frontend Browse request
      |
      v
DA Runtime Browse
      |
      v
OPC DA Server
```

동적 address space를 가진 서버에서도 source가 현재 제공하는 내용을 기준으로 한다.

---

## 15.4 Browse state serialization

DA 2.x Browse는 current browse position을 사용하는 stateful API다.

따라서 동시에 여러 request가 같은 browse object의 position을 변경하면 잘못된 결과가 나올 수 있다.

v0에서는 Browse를 dedicated DA thread에서 **직렬화**한다.

각 HTTP Browse request는 가능한 한:

1. root position으로 reset
2. request의 browse path를 순서대로 이동
3. 해당 position에서 enumerate
4. 결과 수집
5. operation 종료

형태로 독립 실행한다.

---

## 15.5 Branch와 Item identity

Item:

- identity의 기준은 **실제 DA ItemID**
- display/browse path에서 ItemID를 추론하지 않는다.

Branch:

- DA Server가 stable item identity를 제공하면 사용
- 그렇지 않으면 Browse frontend에서 navigation을 위해 browse-name sequence를 사용할 수 있다.
- 이 sequence는 Adapter의 **navigation representation**이지 source semantic identity가 아니다.

---

# 16. Read

## 16.1 Batch first

내부 API는 단건보다 batch를 기본으로 한다.

```go
ReadBatch(ctx, []ItemID, ReadOptions) -> []DAReadResult
```

HTTP 단건 read도 내부적으로 batch 1개로 처리할 수 있다.

---

## 16.2 Read source

DA 2.05a capability를 숨기지 않기 위해 내부적으로 `device`와 `cache` source 개념을 표현할 수 있다.

v0 HTTP 기본값은 correctness를 위해 `device`를 우선한다.

향후 API가 `cache`를 노출하더라도 이는 DA 자체 capability를 노출하는 것이므로 scope 위반이 아니다.

---

## 16.3 부분 실패

Batch 요청에서 일부 item이 실패했다고 전체를 generic 500으로 만들지 않는다.

예:

```text
Item A -> S_OK
Item B -> OPC_E_UNKNOWNITEMID
Item C -> S_OK
```

결과도 item별로 유지한다.

---

# 17. Write

## 17.1 Write는 source write-through다

```text
Frontend Write
   |
   v
DA Runtime Write
   |
   v
IOPCSyncIO::Write
   |
   v
DA Server
```

중간 queue가 있더라도 semantic transformation을 하지 않는다.

---

## 17.2 DA 2.05a Write 범위

v0의 DA 2.05a Write는 **Value Write**를 대상으로 한다.

Status/Quality/Timestamp를 write하는 기능을 가짜로 제공하지 않는다.

DA 3.0 `WriteVQT` 지원은 별도 확장이다.

---

## 17.3 Write 기본 보안 정책

산업 장비에서 Write는 실제 제어 동작으로 이어질 수 있다.

따라서:

- 기능 자체는 구현한다.
- 기본 설정은 `write disabled`.
- 사용자가 명시적으로 enable 해야 한다.
- Adapter는 Write를 자동 재시도하지 않는다.
- timeout 이후 “성공 여부 불명” 상태가 될 수 있음을 명시한다.
- rollback을 약속하지 않는다.

---

## 17.4 Type validation

JSON number를 무조건 DA canonical type으로 임의 변환하지 않는다.

Write request는 type ambiguity를 제거해야 한다.

권장 request:

```json
{
  "itemId": "Random.Int2",
  "dataType": "VT_I2",
  "value": 42
}
```

Runtime은:

- transport value가 요청 VARTYPE으로 lossless 표현 가능한지 확인
- source canonical type과의 관계 확인
- 지원되지 않는 conversion이면 실패

silent narrowing/overflow 금지.

---

# 18. Subscribe — 향후 Core Capability

Subscribe는 UA만의 기능이 아니다.

장기적으로는 DA Runtime 자체 capability로 정의한다.

```text
DA Runtime Subscribe
        |
        +----> gRPC server stream
        |
        +----> OPC UA Subscription/MonitoredItem
        |
        +----> 필요한 경우 향후 HTTP streaming frontend
```

DA Browse 때문에 subscription을 만들지 않는다.

---

## 18.1 DA side

향후 DA 2.x subscription은 `IOPCDataCallback` / connection point 기반으로 구현하는 방향을 사용한다.

이때 callback COM object와 thread/apartment/message pump correctness가 중요하다.

---

## 18.2 v0에서는 제외

초기 HTTP 검증 단계에서는 Subscribe를 구현하지 않는다.

v0 목표는:

- Connect
- Browse
- Read
- Write
- VARTYPE
- Quality
- Timestamp
- HRESULT
- reconnect

검증이다.

---

# 19. DA-native Internal Contract

Frontend 독립적인 Core contract를 정의한다.

---

## 19.1 DAItemID

```go
type DAItemID string
```

원본 문자열을 canonicalize하지 않는다.

다음 변환 금지:

- trim
- lower/upper case
- delimiter change
- dot/slash replacement
- whitespace normalization

source가 준 값 그대로 identity로 사용한다.

---

## 19.2 DAVarType

단순 Go reflect type으로 대체하지 않는다.

반드시 원래 COM VARTYPE을 보존한다.

예:

```text
VT_I2
VT_I4
VT_R4
VT_R8
VT_BSTR
VT_BOOL
...
```

이유:

```text
VT_I2 -> Go int -> source type 손실
VT_I4 -> Go int -> source type 손실
```

같은 문제를 막기 위해서다.

내부 representation은 최소한 다음을 가져야 한다.

```text
Raw VARTYPE code
Symbolic name if known
Array/byref flags
```

---

## 19.3 DAValue

개념적 모델:

```go
type DAValue struct {
    ItemID           DAItemID
    VarType          DAVarType
    Value            TypedValue
    QualityRaw       uint16
    Timestamp        time.Time
    TimestampPresent bool
    HRESULT          int32
}
```

실제 구현에서는 COM VARIANT ownership과 Go representation을 분리한다.

---

## 19.4 Quality

DA Quality는 raw 16-bit 값을 보존한다.

```text
lower 8 bits: QQSSSSLL
upper 8 bits: vendor-specific
```

Core/HTTP/gRPC에서는 raw 16-bit 값을 잃지 않는다.

해석 helper를 제공하더라도 raw가 source of truth다.

---

## 19.5 Timestamp

source timestamp가 존재하면 그대로 보존한다.

Adapter processing time을 source timestamp로 대체하지 않는다.

필요하다면 별도 field로 adapter observation time을 둘 수 있으나 명확히 구분한다.

---

## 19.6 HRESULT

HRESULT는 generic string error로 없애지 않는다.

다음 둘을 구분한다.

1. COM method-level HRESULT
2. item-level HRESULT array

성공 판단은 `hr == 0`만 보지 않고 COM `SUCCEEDED/FAILED` semantics를 따른다.

raw HRESULT도 보존한다.

---

## 19.7 Access Rights

source가 제공한 Read/Write access rights를 보존한다.

Frontend가 이를 표시할 수 있으면 그대로 노출한다.

단, access rights를 보고 Adapter가 새로운 authorization semantics를 만들지는 않는다.

---

# 20. VARIANT와 값 표현

## 20.1 원칙

Go type으로 변환한 뒤 source VARTYPE을 잃어버리는 구현은 금지한다.

---

## 20.2 v0 scalar 우선

v0에서는 실제 현장 검증에 필요한 scalar VARTYPE부터 지원한다.

예상 우선순위:

- signed/unsigned integer
- float/double
- boolean
- BSTR
- 필요한 날짜/decimal type

지원 범위는 테스트 결과에 따라 명시적으로 관리한다.

---

## 20.3 Unsupported VARTYPE

지원하지 않는 타입은:

```text
UNSUPPORTED_VARTYPE
raw vartype code
item id
```

를 반환한다.

다른 type으로 변환하지 않는다.

---

## 20.4 SAFEARRAY

SAFEARRAY를 구현할 때 다음 정보를 잃지 않아야 한다.

- element VARTYPE
- dimensions
- lower bound
- length per dimension
- element values

따라서 “JSON array로 flat하게 만들기”는 허용되지 않는다.

v0에서 완전한 SAFEARRAY support가 없다면 명시적으로 unsupported 처리한다.

---

## 20.5 COM memory ownership

장시간 안정성을 위해 ownership은 코드 리뷰 핵심 항목이다.

확인 대상:

- `IUnknown::Release`
- `CoTaskMemFree`
- `VariantClear`
- BSTR ownership / `SysFreeString`
- SAFEARRAY lifecycle
- enumerator Release
- returned buffer ownership
- callback reference counting

COM resource leak은 “GC가 언젠가 정리한다”는 방식으로 처리하지 않는다.

---

# 21. Frontend Architecture

Frontend의 역할:

1. transport request decode
2. DA-native request로 변환
3. DA Runtime 호출
4. DA-native result를 transport representation으로 encode

Frontend가 하지 않는 것:

- source routing
- data transformation
- tag mapping
- tag renaming
- normalization
- caching semantics 변경
- retry semantics 변경

---

# 22. Frontend Admission Rule

새 Frontend를 추가하기 위한 필수 조건:

1. DA Browse/Read/Write/Subscribe를 직접 표현할 수 있다.
2. ItemID/VARTYPE/Quality/Timestamp/HRESULT를 보존할 수 있다.
3. 새로운 산업 data model을 요구하지 않는다.
4. persistent storage를 요구하지 않는다.
5. routing/topic semantics를 요구하지 않는다.
6. source가 OPC DA 하나인 구조를 깨지 않는다.
7. 다른 Frontend와 Core behavior가 달라지지 않는다.

이 조건을 만족하지 못하면 별도 프로젝트다.

---

# 23. Frontend 범위

## 23.1 승인된 방향

제품 방향상 다음 Frontend는 자연스러운 확장 후보다.

- HTTP/JSON
- gRPC
- OPC UA

---

## 23.2 조건부 후보

실제 요구가 확인될 때만 검토:

- WebSocket
- Named Pipe / local IPC
- XML-DA
- custom TCP

---

## 23.3 제외

다음은 기본적으로 제외한다.

- MQTT
- Kafka
- NATS
- GraphQL
- AMQP
- event bus

이들은 단순 access transport가 아니라 topic/event/query semantics를 요구하기 쉽기 때문이다.

---

# 24. v0의 목적

v0는 “완성된 산업용 Gateway”가 아니다.

**DA Runtime 자체의 기술적 검증**이 목적이다.

검증 질문:

- Go에서 direct COM OPC DA 연결이 안정적인가?
- 실제 DA Server에서 Browse가 되는가?
- Item registration이 올바른가?
- Batch Read가 올바른가?
- Write가 올바른가?
- VARTYPE이 보존되는가?
- Quality가 raw 16-bit로 보존되는가?
- Timestamp가 보존되는가?
- HRESULT가 item별로 보존되는가?
- reconnect 후 stale handle이 재사용되지 않는가?
- x86/x64에서 어떻게 다르게 동작하는가?
- 장시간 실행 시 COM resource leak이 없는가?

---

# 25. v0 Frontend: HTTP/JSON

HTTP를 먼저 선택한 이유:

- curl/Postman으로 즉시 검증 가능
- 별도 client SDK가 필요 없음
- `.proto`/stub generation 불필요
- DA Runtime debugging이 쉬움
- Frontend implementation cost가 작음
- 값/type/quality/error를 사람이 직접 확인하기 쉬움

HTTP가 제품의 “최종 주력 Frontend”라는 의미는 아니다.

---

# 26. v0 HTTP API

초기 endpoint:

```text
GET  /v1/status
POST /v1/browse
POST /v1/read
POST /v1/write
```

Subscribe endpoint는 v0에 없다.

---

# 27. GET /v1/status

목적:

- Adapter 상태
- DA 연결 상태
- source capability
- HTTP listener 상태
- write enable 상태

예:

```json
{
  "state": "connected",
  "source": {
    "progId": "Matrikon.OPC.Simulation.1",
    "clsid": "{...}",
    "connectionGeneration": 3
  },
  "capabilities": {
    "browse": "supported",
    "read": true,
    "write": true,
    "subscribe": false
  },
  "writeEnabled": false,
  "frontend": {
    "http": {
      "listening": true
    }
  }
}
```

process value를 status에 포함하지 않는다.

---

# 28. POST /v1/browse

## 28.1 Request

Browse position을 server-side session으로 유지하지 않는다.

Frontend navigation용 path sequence를 request에 전달한다.

```json
{
  "path": ["Channel1", "Device1"],
  "filter": "all"
}
```

`path=[]`는 root다.

`path`는 source의 semantic ItemID가 아니라 DA Browse navigation representation이다.

---

## 28.2 Response

```json
{
  "path": ["Channel1", "Device1"],
  "entries": [
    {
      "kind": "branch",
      "name": "GroupA",
      "itemId": null
    },
    {
      "kind": "item",
      "name": "Temperature",
      "itemId": "Channel1.Device1.Temperature",
      "dataType": {
        "code": 4,
        "name": "VT_R4"
      },
      "accessRights": {
        "read": true,
        "write": false
      }
    }
  ]
}
```

source가 metadata를 제공하지 않는 경우 해당 field는 `null/unknown`으로 둔다.

값을 추론해 채우지 않는다.

---

## 28.3 Browse result limit

Browse response는 반드시 bounded 해야 한다.

v0에서는 continuation을 억지로 구현하기보다 결과가 hard limit을 넘으면 명시적으로 실패할 수 있다.

```text
BROWSE_RESULT_LIMIT_EXCEEDED
```

silent truncation은 금지한다.

제품화 과정에서 continuation을 구현할 경우 dynamic source semantics와 COM enumerator lifetime을 고려한 별도 ADR이 필요하다.

---

# 29. POST /v1/read

## 29.1 Request

```json
{
  "source": "device",
  "items": [
    { "itemId": "Random.Int2" },
    { "itemId": "Random.Real8" }
  ]
}
```

---

## 29.2 Response

응답의 `results` 순서는 request의 `items` 순서와 대응해야 한다. Adapter가 최적화를 위해 사용자 요청을 임의로 재정렬하거나 실패 item을 제거하지 않는다.

```json
{
  "results": [
    {
      "itemId": "Random.Int2",
      "ok": true,
      "dataType": {
        "code": 2,
        "name": "VT_I2"
      },
      "valueEncoding": "json",
      "value": 42,
      "quality": 192,
      "timestamp": "2026-08-22T12:30:00.1234567Z",
      "timestampPresent": true,
      "hresult": {
        "value": 0,
        "hex": "0x00000000"
      }
    },
    {
      "itemId": "Missing.Item",
      "ok": false,
      "hresult": {
        "value": -1073479672,
        "hex": "0xC0040008"
      }
    }
  ]
}
```

숫자는 예시일 뿐 특정 HRESULT를 contract로 고정하지 않는다.

---

# 30. JSON Value Encoding

JSON은 COM VARIANT의 모든 값을 자연스럽게 lossless 표현하지 못한다.

따라서 transport representation rule을 명시한다.

| DA type class | JSON representation |
|---|---|
| BOOL | JSON boolean |
| I1/UI1/I2/UI2/I4/UI4 | JSON integer |
| I8/UI8 | decimal string |
| R4/R8 finite | JSON number |
| R4/R8 NaN/Inf | explicit special-float encoding 또는 unsupported until implemented |
| BSTR | JSON string |
| DECIMAL | exact decimal string |
| DATE | explicit OLE DATE representation 또는 명시적 supported mapping |
| SAFEARRAY | v0 unsupported unless full dimensional metadata preserved |
| unknown/custom | explicit unsupported |

핵심:

> JSON 편의를 위해 source type을 다른 type으로 강제 변환하지 않는다.

---

# 31. POST /v1/write

## 31.1 Request

```json
{
  "items": [
    {
      "itemId": "Random.Int2",
      "dataType": "VT_I2",
      "valueEncoding": "json",
      "value": 42
    }
  ]
}
```

---

## 31.2 Response

Write 결과 역시 request item과 대응 가능한 순서를 유지한다. Adapter는 처리량을 위해 Write를 임의 재정렬하거나 서로 다른 request의 Write를 의미적으로 병합하지 않는다.

```json
{
  "results": [
    {
      "itemId": "Random.Int2",
      "ok": true,
      "hresult": {
        "value": 0,
        "hex": "0x00000000"
      }
    }
  ]
}
```

---

## 31.3 Write disabled

write가 비활성화되어 있다면 request를 source에 전달하지 않는다.

예:

```text
HTTP 403
WRITE_DISABLED
```

---

# 32. HTTP Error Boundary

## 32.1 Request-level failure

예:

- malformed JSON
- invalid schema
- batch size limit 초과
- Adapter not ready
- write disabled
- internal runtime unavailable

이 경우 HTTP status로 표현한다.

---

## 32.2 Item-level failure

DA가 특정 item만 실패시킨 경우:

```text
HTTP request 자체는 성공적으로 처리
+
results[] item별 ok/hresult
```

partial failure를 generic 500으로 숨기지 않는다.

---

## 32.3 권장 HTTP status

| 상황 | Status |
|---|---:|
| 정상 batch/partial item failure | 200 |
| malformed request | 400 |
| write disabled | 403 |
| source capability상 Browse 불가 | 422 |
| DA Runtime disconnected/unavailable | 503 |
| Adapter-level request deadline | 504 |
| 구현 bug/unexpected internal failure | 500 |

정확한 error body schema는 구현 시 고정하되 DA HRESULT와 Adapter error를 구분한다.

---

# 33. Timeout과 Cancellation

## 33.1 HTTP disconnect != COM call cancellation

sync COM method가 이미 실행 중일 때 HTTP client가 연결을 끊었다고 COM thread를 강제로 죽이지 않는다.

금지:

- `TerminateThread`
- unsafe COM object destruction
- 성공 여부가 불명확한 Write 강제 중단

---

## 33.2 queued request

아직 COM thread에서 시작되지 않은 요청은 context cancellation로 제거할 수 있다.

---

## 33.3 in-flight request

이미 COM call이 실행 중이라면:

- 결과를 기다리거나
- frontend deadline은 먼저 끝날 수 있다.
- 결과는 client에게 버려질 수 있다.
- Write는 자동 재시도하지 않는다.

---

## 33.4 COM hang

COM call 자체가 무한 block되는 vendor failure는 중요한 운영 위험이다.

v0에서는 thread 강제 종료로 “복구한 척”하지 않는다.

가능한 정책:

- watchdog으로 degraded 상태 표시
- 신규 요청 fail-fast
- 운영자가 process restart

향후 hard isolation이 필요하다면 DA Runtime subprocess 분리를 별도 설계로 검토할 수 있으나 v0 범위가 아니다.

---

# 34. gRPC Frontend — 향후

gRPC의 목적:

- typed client API
- Go/Java/.NET 등 서비스 연동
- Subscribe의 server streaming

예상 service:

```text
Browse
Read
Write
Subscribe
Status
```

protobuf도 DA-native field를 유지한다.

예:

```text
item_id
vartype_raw
quality_raw
timestamp
timestamp_present
hresult
typed value
```

Asset/Metric protobuf 모델을 만들지 않는다.

---

# 35. OPC UA Frontend — 향후

OPC UA Frontend는 Access Adapter의 여러 Frontend 중 하나다.

구조:

```text
Existing OPC DA Server
        |
        v
   DA Runtime
        |
  DA-native Contract
        |
        v
  Minimal UA Server
        |
        v
Existing UA Client
```

---

## 35.1 역할

Adapter는 UA side에서 **UA Server** 역할을 한다.

DA side에서는 **DA Client** 역할을 한다.

---

## 35.2 Address Space

기본 mapping:

```text
DA Branch -> UA Folder/Object
DA Item   -> UA Variable
```

Item identity는 exact DA ItemID를 기준으로 한다.

DA Browse path를 ItemID라고 가정하지 않는다. DA Server의 delimiter를 추측해서 ItemID를 재구성하지도 않는다.

UA identity 규칙:

- Namespace URI는 Adapter가 안정적으로 유지한다.
- `ns=2` 같은 Namespace Index 숫자를 persistent identity로 간주하지 않는다.
- Item node의 NodeId는 가능한 한 exact DA ItemID와 직접 대응시킨다.
- Branch는 source가 stable identity를 제공하면 그것을 우선하고, 그렇지 않으면 navigation representation을 사용한다.
- BrowseName/DisplayName은 DA Browse가 제공한 이름을 우선 사용한다.
- 사용 편의를 위해 tag 이름을 임의로 정리하거나 `.`/공백을 `_`로 치환하지 않는다.

---

## 35.3 표준 mapping

OPC UA Part 8의 COM DA mapping을 기준으로 representation한다.

예:

```text
VT_I2 -> Int16
VT_I4 -> Int32
VT_R4 -> Float
VT_R8 -> Double
...
```

DA Quality:

```text
raw DA quality -> standard UA StatusCode mapping
```

DA Timestamp:

```text
DA Timestamp -> UA SourceTimestamp
```

HRESULT:

```text
DA error -> UA StatusCode
```

---

## 35.4 Quality vendor bits

DA Quality의 upper 8 bit vendor-specific 부분은 Core에서는 보존한다.

그러나 표준 COM DA→UA mapping에서는 vendor quality가 UA StatusCode로 완전히 보존되지 않을 수 있다.

UA Frontend는 표준 mapping을 따르며, 단지 raw quality를 보존하기 위해 임의의 custom UA property를 추가하지 않는다.

이 손실은 documentation에 기록한다.

---

## 35.5 UA 구현 범위

처음 UA Frontend를 구현할 때 최소 vertical slice:

- UA-TCP Hello/Acknowledge
- message framing/chunking
- UA Binary
- minimum SecureChannel
- endpoint discovery
- Session create/activate/close
- required AddressSpace
- Browse/BrowseNext
- Read
- Write

Subscribe가 DA Runtime에 추가된 후:

- Subscription
- MonitoredItem
- Publish

를 추가한다.

UA ServerTimestamp가 필요한 경우 Adapter가 해당 operation을 처리한 시간을 사용할 수 있으나, 이것을 DA SourceTimestamp와 혼동하지 않는다. DA Timestamp는 SourceTimestamp의 source of truth다.

직접 UA wire parser를 구현하는 경우 다음은 필수다.

- max message size
- max chunk count
- max array length
- max string/ByteString length
- max nesting/decode depth
- max sessions/subscriptions/monitored items
- malformed packet 처리
- integer overflow/length overflow 방지
- fuzz test

직접 구현한다는 이유로 UA protocol validation을 느슨하게 하지 않는다.

---

## 35.6 UA security

초기 interop prototype에서 `SecurityPolicy=None + Anonymous`를 사용할 수는 있으나:

- local/test 전용
- production-ready라고 표현 금지
- 첫 실제 운영 릴리스 전에 certificate lifecycle과 secure policy 필요

---

## 35.7 Conformance claim

공식 인증을 받지 않은 상태에서 다음 표현을 사용하지 않는다.

- OPC UA Certified
- OPC UA Compliant
- OPC Foundation Certified

또한 구현한 subset이 제한되어 있는 동안 README에서 “모든 OPC UA Client와 호환된다”는 식으로 일반화하지 않는다.

대신 구현한 subset과 실제 interoperability test 결과를 명확히 기록한다. OPC Foundation의 로고나 인증 마크를 임의의 프로젝트 배지처럼 사용하지 않는다.

---

# 36. Security Model

## 36.1 새로운 network attack surface

DA는 기존에는 local COM으로만 사용되었을 수 있다.

Adapter가 HTTP/gRPC/UA를 열면 새로운 network attack surface가 생긴다.

따라서 “단순 relay니까 보안이 중요하지 않다”는 판단은 금지한다.

---

## 36.2 v0 HTTP

v0는 DA 검증 목적이다.

기본 정책:

- loopback bind가 기본
- remote bind는 명시적 설정
- write disabled가 기본
- production security claim 없음

---

## 36.3 Remote exposure

향후 remote network 사용 시 다음이 필요하다.

- TLS
- authentication 전략
- request limits
- connection limits
- audit/logging boundary
- write authorization boundary

단, RBAC platform으로 확장하지 않는다.

정확한 authentication mechanism은 별도 ADR로 결정한다.

---

# 37. Resource Limits

직접 protocol/COM 데이터를 다루므로 hard limit가 필요하다.

반드시 bounded 해야 하는 항목:

- HTTP request body size
- items per Read batch
- items per Write batch
- Browse entry count
- ItemID length
- BSTR length
- array size
- SAFEARRAY dimensions
- concurrent frontend requests
- queued DA commands
- recent diagnostic operations
- log size/rotation

정확한 default는 benchmark와 실제 server 검증 후 결정한다.

**무제한을 default로 두지 않는다.**

---

# 38. Observability

관찰 기능의 목적은 Adapter 동작 확인이지 process data 분석이 아니다.

허용:

- DA connection state
- connection generation
- Frontend listener state
- request count
- error count
- reconnect count
- queue depth
- operation latency
- bounded recent operation metadata

금지:

- value trend chart
- historian
- value history
- alarm engine
- process dashboard
- persisted telemetry

---

# 39. Logging

기본 log에는 process value를 남기지 않는다.

권장 log field:

```text
timestamp
level
operation
item_count
duration
result
hresult
connection_generation
```

ItemID도 운영 환경에서 민감할 수 있으므로 verbose/debug policy를 별도로 검토한다.

Write value를 평문 log로 남기지 않는다.

---

# 40. Persistence

프로세스 재시작 후 복원해야 하는 process state는 없다.

가능한 persistent file:

- config
- logs
- certificate/key (secure frontend 도입 후)
- compatibility/test artifact

금지:

- cached values
- tag DB
- persisted browse tree
- write queue replay log
- historian

특히 Write를 재시작 후 replay하지 않는다.

---

# 41. Configuration

v0 config의 핵심:

```text
source ProgID/CLSID
HTTP listen address
write enabled
timeouts
resource limits
logging
```

금지되는 config:

```text
tag mapping table
tag rename table
scaling
unit conversion
asset model
multi-server routing
```

---

# 42. Server Discovery

OPCEnum 등을 이용한 DA Server 자동 discovery는 v0 핵심이 아니다.

초기에는 ProgID 또는 CLSID를 명시한다.

Discovery가 향후 추가되더라도:

- convenience 기능일 뿐
- runtime architecture를 바꾸지 않음
- remote DCOM discovery로 확장하지 않음

---

# 43. Concurrency

Frontend는 concurrent request를 받을 수 있다.

하지만 COM side는 초기에는 직렬 실행한다.

```text
many HTTP requests
        |
        v
bounded queue
        |
        v
single DA runtime OS thread
```

장점:

- apartment correctness
- Browse position 안전
- handle lifecycle 단순
- vendor compatibility 우선
- race condition 최소화

성능 문제가 실제 측정되기 전에는 parallel COM access로 확장하지 않는다.

---

# 44. Backpressure

DA command queue는 bounded 해야 한다.

queue가 가득 차면:

- 무한 대기 금지
- memory unbounded growth 금지
- frontend에 overload/unavailable 응답

Read/Write 요청을 임의 drop하지 않는다.

---

# 45. Frontend 간 일관성

향후 HTTP, gRPC, UA가 동시에 존재할 때도 source behavior는 하나다.

금지:

```text
HTTP -> cached read
gRPC -> device read
UA -> custom normalized value
```

각 Frontend가 다른 semantic path를 가져서는 안 된다.

옵션이 존재한다면 DA-native option으로 명시적으로 요청되어야 한다.

---

# 46. Failure Model

오류는 세 층으로 구분한다.

## 46.1 Transport/Frontend error

예:

- malformed JSON
- invalid protobuf
- UA decoding error

---

## 46.2 Adapter/Runtime error

예:

- queue full
- runtime stopped
- unsupported VARTYPE
- browse unsupported
- write disabled
- timeout

---

## 46.3 DA source error

예:

- COM activation failure
- method HRESULT failure
- per-item HRESULT
- Bad Quality
- invalid ItemID
- bad rights
- server disconnected

이 세 층을 하나의 generic error로 합치지 않는다.

---

# 47. Stale Data Policy

연결이 끊겼을 때 이전 Read 결과를 저장해두었다가 반환하지 않는다.

금지:

```text
DA disconnected
    |
return last value as Good
```

대신:

```text
DA disconnected
    |
explicit unavailable/error
```

필요하다면 이전 값은 observability/debug context로만 존재할 수 있으며 정상 Read 결과로 사용하지 않는다.

---

# 48. Correctness 원칙

다음은 성능보다 우선한다.

- exact ItemID
- exact VARTYPE
- raw Quality
- source Timestamp
- raw HRESULT
- per-item failure
- COM apartment correctness
- ownership correctness
- reconnect generation correctness

---

# 49. 성능 원칙

성능 최적화는 다음 순서를 따른다.

1. correctness
2. interoperability
3. leak-free long run
4. batching
5. handle reuse
6. bounded cache
7. profiling
8. 필요 시 concurrency

처음부터 복수 COM thread나 complicated lock-free 구조를 만들지 않는다.

---

# 50. 테스트 전략

## 50.1 Unit Test

필수 대상:

- HRESULT success/failure 판단
- HRESULT hex/decimal representation
- VARTYPE parsing
- numeric overflow detection
- JSON value encoding
- Quality raw preservation
- Timestamp conversion
- connection generation
- item handle invalidation
- batch partial failure model
- write disabled
- queue backpressure
- unsupported VARTYPE

---

## 50.2 COM marshalling test

Windows-specific test:

- VARIANT read/write
- BSTR
- integer widths
- float widths
- BOOL
- memory cleanup
- interface Release
- CoTaskMem memory

---

## 50.3 Integration Test

실제 DA Server에서 검증한다.

최소 시나리오:

- connect
- disconnect
- reconnect
- Browse root
- nested Browse
- known ItemID Read
- batch Read
- Bad Quality
- invalid ItemID
- Write success
- Write denied
- type mismatch
- server restart
- stale handle invalidation

---

## 50.4 Architecture별 test

다음 조합을 별도로 확인한다.

```text
windows/386
windows/amd64
```

---

## 50.5 Vendor Compatibility

가능하다면 서로 다른 구현을 사용한다.

예시 후보:

- Matrikon simulation/server
- Kepware
- 실제 legacy vendor server

특정 vendor 한 개에서 성공했다고 “OPC DA compatible”을 일반화하지 않는다.

---

## 50.6 Soak Test

장시간 테스트에서 확인:

- process memory
- COM reference count 추정 지표
- handle count
- goroutine count
- queue growth
- reconnect 반복
- repeated Browse
- repeated Add/Remove item
- repeated Write

목표는 단순 “크래시 없음”이 아니라 **증가가 계속되는 resource leak이 없는지** 확인하는 것이다.

---

# 51. HTTP Acceptance Test

curl/Postman만으로 최소 검증 가능해야 한다.

예:

```text
1. status
2. browse
3. read known item
4. read multiple items
5. invalid item
6. write disabled
7. enable write
8. write
9. server stop
10. read -> unavailable
11. server restart
12. automatic reconnect
13. read -> success
```

---

# 52. Compatibility Matrix

Repository에는 실제 검증 결과를 기록한다.

예:

| DA Server | Version | Windows | Adapter Arch | Browse | Read | Write | Notes |
|---|---|---|---|---|---|---|---|
| Matrikon ... | ... | ... | amd64 | PASS | PASS | PASS | ... |

향후 Frontend:

| Client | Version | Frontend | Browse | Read | Write | Subscribe |
|---|---|---|---|---|---|---|

“지원”은 추측이 아니라 실제 검증 결과로 관리한다.

---

# 53. CI

초기 CI:

```text
gofmt check
go test
go vet
static analysis
windows/386 build
windows/amd64 build
```

COM integration test는 일반 GitHub-hosted runner에서 실제 DA Server가 없을 수 있으므로 별도 환경 또는 self-hosted test가 필요할 수 있다.

단순 cross-build 성공을 DA interoperability 검증으로 간주하지 않는다.

---

# 54. Release

장기적으로 목표:

- windows/386 binary
- windows/amd64 binary
- checksums
- version metadata
- changelog/release notes
- reproducible build에 가까운 절차

초기에는 installer보다 단일 binary 실행을 우선한다.

Windows Service packaging은 core가 안정된 후 검토한다.

---

# 55. UI

v0에 GUI는 없다.

향후 운영 UI를 만들더라도 범위는 제한한다.

허용:

- connection state
- listener state
- request/error counters
- config status
- recent operation metadata

금지:

- value dashboard
- trend
- chart
- historian
- alarm
- control panel

---

# 56. OSS 정책

프로젝트는 GitHub public open-source repository로 공개한다.

초기 repository 후보:

```text
opc-da-access-adapter
```

표시명:

```text
OPC DA Access Adapter
```

설명 예:

> A thin access adapter for exposing OPC DA through modern interfaces without changing source semantics.

---

# 57. License

오픈소스 license는 최종 확정이 필요하다.

현재 권장 후보:

```text
Apache License 2.0
```

그러나 이것은 아직 최종 결정으로 간주하지 않는다.

Repository 생성 시 license를 명시적으로 확정한다.

---

# 58. 기존 프로젝트 코드 사용 정책

기존 OSS는 다음 목적으로 활용할 수 있다.

- behavior reference
- interoperability comparison
- bug/edge-case discovery
- client/server test target

기존 코드를 가져올 경우 반드시 개별 license와 attribution을 확인한다.

이 프로젝트가 direct implementation을 지향한다는 이유로 license 의무가 사라지지 않는다.

공식 specification 문서를 repository에 복제하지 않는다.

---

# 59. 프로젝트의 차별점

이 프로젝트의 가치는 “Frontend 개수가 많다”가 아니다.

핵심 가치 후보:

1. OPC DA source 하나에만 집중
2. DA-native semantics preservation
3. 별도 common industrial model 없음
4. Tag mapping 없음
5. Data transformation 없음
6. 작은 deployment
7. direct DA Runtime
8. Frontend가 DA capability를 그대로 노출
9. 실제 vendor compatibility를 공개 검증

---

# 60. Gateway와의 경계

Gateway가 되어가는 징후:

```text
"Modbus도 넣자"
"서버 여러 개를 하나로 묶자"
"tag 이름 바꾸자"
"unit convert 하자"
"MQTT도 publish 하자"
"DB에 저장하자"
"asset model 만들자"
"rule도 넣자"
```

이 중 하나라도 제안되면 먼저 다음 질문을 해야 한다.

> 이것이 OPC DA에 접근하는 방법을 제공하기 위해 반드시 필요한가?

아니라면 scope 밖이다.

---

# 61. 기능 추가 Decision Test

새 기능을 넣기 전에 다음을 모두 확인한다.

1. Source는 여전히 OPC DA 하나인가?
2. source data의 의미를 바꾸지 않는가?
3. DA capability에 직접 대응하는가?
4. persistent process data를 요구하지 않는가?
5. routing/aggregation을 요구하지 않는가?
6. common model을 만들지 않는가?
7. 기존 Frontend의 semantics를 바꾸지 않는가?
8. 실제 사용자 요구가 존재하는가?

하나라도 명확히 실패하면 별도 프로젝트 또는 NO-GO다.

---

# 62. 단계별 구현 계획

## Phase 0 — Repository / Skeleton

목표:

- OSS repository
- Go module
- Windows build
- CI
- scope/design docs
- empty DA Runtime lifecycle
- HTTP status endpoint

구현하지 않음:

- fake data production behavior

---

## Phase 1 — COM Foundation

목표:

- dedicated OS thread
- COM init/uninit
- ProgID/CLSID activation
- connect/disconnect
- correct Release/memory helpers
- status

Acceptance:

- repeated start/stop leak 확인
- x86/x64 build

---

## Phase 2 — DA Read Core

목표:

- AddGroup
- AddItems
- handle cache
- VARTYPE
- AccessRights
- `IOPCSyncIO::Read`
- item-level HRESULT
- Quality
- Timestamp

HTTP:

```text
POST /v1/read
```

이 단계가 첫 핵심 milestone이다.

---

## Phase 3 — Browse

목표:

- capability detection
- `IOPCBrowseServerAddressSpace`
- serialized browse
- path navigation
- branch/item distinction
- exact ItemID

HTTP:

```text
POST /v1/browse
```

Browse unsupported server에서도 known ItemID Read는 계속 동작해야 한다.

---

## Phase 4 — Write

목표:

- strict typed write
- `IOPCSyncIO::Write`
- per-item HRESULT
- write disabled default
- no auto-retry

HTTP:

```text
POST /v1/write
```

---

## Phase 5 — Reliability

목표:

- reconnect
- generation invalidation
- server restart scenario
- queue bounds
- timeout behavior
- soak tests
- resource leak fixes
- multiple vendor verification

---

## Phase 6 — Next Frontend

DA Runtime이 실제로 안정화된 후에만 선택한다.

유력:

```text
gRPC
```

이유:

- typed API
- future Subscribe streaming

단, 실제 수요가 없다면 추가하지 않을 수 있다.

---

## Phase 7 — Subscribe

DA core Subscribe를 먼저 구현한다.

그 후:

- gRPC stream
- UA Subscription

등으로 노출한다.

---

## Phase 8 — OPC UA Frontend

DA Runtime과 mapping이 안정된 이후 진행한다.

UA implementation cost가 DA Runtime 검증을 방해하지 않도록 뒤로 둔다.

---

# 63. v0 명시적 Non-goals

v0에서 하지 않는다.

- OPC UA
- gRPC
- Subscribe
- WebSocket
- SSE
- MQTT
- Kafka
- UI
- DB
- Historian
- Tag Mapping
- Tag rename
- Scaling
- Normalization
- Asset Model
- Multi DA Server
- Remote DCOM
- DCOM Tunneling
- OPC DA 3.0 완전 지원
- full SAFEARRAY support를 검증 전 약속
- production auth/RBAC
- arbitrary plugin framework

---

# 64. Plugin Framework를 만들지 않는다

Frontend 확장 가능하다는 이유로 처음부터 다음을 만들지 않는다.

```go
type Plugin interface { ... }
type FrontendRegistry struct { ... }
dynamic plugin loader
plugin marketplace
runtime extension API
```

Frontend는 source tree에 명시적으로 구현한다.

실제 plugin 필요성이 확인되지 않는 한 plugin system은 과설계다.

---

# 65. 공개 API Versioning

HTTP:

```text
/v1/...
```

를 사용한다.

v1이 공개 stable로 선언되기 전에는 API가 experimental일 수 있다.

한번 stable release로 선언하면:

- field 삭제
- 의미 변경
- type 변경

은 major compatibility break로 취급한다.

---

# 66. HTTP Client Retry 주의

Read/Browse는 client가 retry할 수 있지만 Adapter가 숨은 retry를 무한 수행하지 않는다.

Write는 특히 주의한다.

> Write response timeout은 Write가 실제 DA Server에 적용되지 않았다는 뜻이 아니다.

따라서 Adapter는 timeout된 Write를 자동 replay하지 않는다.

이 내용은 API 문서에 명확히 기록한다.

---

# 67. Process Shutdown

정상 종료 순서:

```text
1. Frontend 신규 요청 중단
2. queued request 처리/취소 정책 적용
3. DA Runtime stop command
4. callback unadvise (future)
5. item/group cleanup
6. COM interface Release
7. CoUninitialize
8. HTTP/gRPC listener 종료
9. process exit
```

COM resource cleanup은 소유한 OS thread에서 수행한다.

---

# 68. Source Metadata

source가 제공하는 property는 “얇음을 위해” 버리지 않는다.

예:

- canonical data type
- access rights
- item properties

단, 이를 Asset metadata로 재구성하지 않는다.

arbitrary vendor property exposure가 실제로 필요해질 경우 `GetProperties` 같은 DA-native capability로 추가할 수 있다.

---

# 69. Naming

코드/문서에서 다음 용어를 우선한다.

사용:

```text
OPC DA Source
DA Runtime
Access Frontend
DA-native Contract
ItemID
VARTYPE
Quality
Timestamp
HRESULT
```

피함:

```text
Asset
Metric
Normalized Tag
Telemetry Model
Protocol Gateway Core
Universal Data Model
```

---

# 70. 알려진 위험

## 70.1 COM correctness

Go와 COM apartment/thread affinity를 잘못 다루면 간헐적인 vendor-specific 오류가 발생할 수 있다.

---

## 70.2 COM memory ownership

VARIANT/BSTR/CoTaskMem/IUnknown ownership 실수는 장시간 memory leak 또는 crash로 이어질 수 있다.

---

## 70.3 Browse vendor differences

Browse가 optional이고 구현 품질도 vendor마다 다를 수 있다.

---

## 70.4 x86/x64

legacy DA 환경은 bitness/registration 차이가 존재할 수 있다.

---

## 70.5 Vendor quirks

표준만 구현했다고 실제 모든 DA Server와 동작하는 것은 아니다.

---

## 70.6 Write safety

네트워크 API의 Write는 실제 설비 제어 surface를 확장할 수 있다.

---

## 70.7 Frontend scope creep

Frontend가 많아질수록 Gateway로 변질될 가능성이 커진다.

Frontend 수가 성공 지표가 되어서는 안 된다.

---

# 71. 성공 기준

프로젝트 성공은 다음으로 판단한다.

좋은 성공 기준:

- 실제 legacy DA Server에서 안정적으로 동작
- 설치가 단순
- source semantics 보존
- compatibility 결과 공개
- 장시간 leak 없음
- 장애 후 자동 회복
- Frontend 결과 일관성

나쁜 성공 기준:

- 지원 protocol 개수
- UI 화면 개수
- integration 개수
- 설정 옵션 개수
- 코드량

---

# 72. 현재 확정된 결정

현재 설계 기준으로 확정된 항목:

- 제품 방향: OPC DA Access Adapter
- Source: OPC DA only
- 구현 언어: Go
- Windows runtime
- local COM 우선
- Remote DCOM/Tunneler 제외
- DA 2.05a baseline
- direct DA implementation
- 기존 OPC DA/UA library를 핵심 구현 dependency로 사용하지 않음
- DA Runtime과 Frontend 분리
- DA-native semantics 보존
- no normalization/transformation
- no common industrial model
- no process-data persistence
- Browse/Read/Write, 향후 Subscribe
- 초기 Frontend: HTTP/JSON
- 이후 후보: gRPC, OPC UA
- COM access dedicated OS thread
- Browse optional capability로 취급
- partial per-item failure 보존
- reconnect 시 stale handle 폐기
- v0 Write value only
- Write disabled by default
- v0 Subscribe 제외
- HTTP는 DA Runtime 검증을 우선
- Gateway화 금지

---

# 73. 아직 최종 확정이 필요한 항목

다음은 구현 전에 또는 해당 기능 착수 전에 ADR로 확정한다.

- OSS license 최종 선택
- repository 최종 이름
- HTTP default port
- exact request/body/batch hard limits
- reconnect backoff default
- COM apartment의 최종 세부 구현(STA message loop 구현 방식)
- scalar VARTYPE v0 exact support matrix
- SAFEARRAY support timing
- production authentication mechanism
- gRPC 도입 시점
- Subscribe 도입 시점
- OPC UA security profile
- Windows Service packaging
- release signing

이 항목들은 “모름”을 숨기기보다 명시적으로 관리한다.

---

# 74. 공식 표준과 외부 참고

구현은 공식 specification을 기준으로 한다.

특히 향후 UA Frontend mapping은 OPC UA Part 8 Annex A의 COM DA→UA mapping을 참고한다.

주요 참고:

- OPC UA Part 8: Data Access — COM DA to UA mapping  
  https://reference.opcfoundation.org/specs/OPC-10000-8/full

- Microsoft — Initializing the COM Library  
  https://learn.microsoft.com/en-us/windows/win32/learnwin32/initializing-the-com-library

- Microsoft — CoInitializeEx  
  https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializeex

- Microsoft — Single-Threaded Apartments  
  https://learn.microsoft.com/en-us/windows/win32/com/single-threaded-apartments

공식 specification 문서를 repository에 복제하지 않는다.

---

# 75. 마지막 요약

이 프로젝트의 구조를 가장 짧게 표현하면 다음과 같다.

```text
                     ONE OPC DA SERVER
                            |
                         Local COM
                            |
                            v
                  +-------------------+
                  |   DA Runtime      |
                  |-------------------|
                  | Browse            |
                  | Read              |
                  | Write             |
                  | Subscribe(future) |
                  | VARTYPE           |
                  | Quality           |
                  | Timestamp         |
                  | HRESULT           |
                  +---------+---------+
                            |
                     DA-native Contract
                            |
                +-----------+-----------+
                |           |           |
                v           v           v
             HTTP        gRPC        OPC UA
             first       future      future
```

그리고 가장 중요한 경계는 다음이다.

```text
SOURCE는 늘리지 않는다.
SEMANTICS는 바꾸지 않는다.
PROCESS DATA를 저장하지 않는다.
COMMON MODEL을 만들지 않는다.
FRONTEND는 접근 방법만 제공한다.
```

프로젝트가 향후 어떤 기능을 추가하더라도 이 다섯 문장이 깨지면 더 이상 OPC DA Access Adapter가 아니다.

---

## Appendix A. 설계 판단 체크리스트

PR 또는 새로운 기능 제안 전에 확인한다.

- [ ] Source가 OPC DA 하나인가?
- [ ] 하나의 instance가 하나의 DA Server를 담당하는가?
- [ ] Remote DCOM/Tunneling을 끌어들이지 않았는가?
- [ ] ItemID를 변경하지 않는가?
- [ ] VARTYPE을 보존하는가?
- [ ] Quality raw value를 보존하는가?
- [ ] Timestamp를 source timestamp로 보존하는가?
- [ ] HRESULT를 숨기지 않는가?
- [ ] partial failure를 보존하는가?
- [ ] unsupported data를 임의 변환하지 않는가?
- [ ] DB/process-data persistence가 추가되지 않았는가?
- [ ] tag mapping/rename/scaling이 추가되지 않았는가?
- [ ] common Asset/Metric 모델이 추가되지 않았는가?
- [ ] Frontend가 새로운ness semantics를 만들지 않는가?
- [ ] COM object가 owning thread 밖으로 유출되지 않는가?
- [ ] COM memory ownership이 명시적인가?
- [ ] queue/cache/result가 bounded인가?
- [ ] Write가 자동 retry/replay되지 않는가?
- [ ] disconnected 상태에서 stale Good 값을 반환하지 않는가?
- [ ] 실제 사용자 요구 또는 compatibility 근거가 있는가?

---

## Appendix B. v0 완료 체크리스트

- [ ] windows/386 build
- [ ] windows/amd64 build
- [ ] dedicated COMd
- [ ] COM init/uninit
- [ ] DA Server connect/disconnect
- [ ] AddGroup
- [ ] AddItems
- [ ] ReadBatch
- [ ] canonical VARTYPE
- [ ] raw Quality
- [ ] source Timestamp
- [ ] per-item HRESULT
- [ ] Browse capability detection
- [ ] Browse if supported
- [ ] known ItemID Read when Browse unsupported
- [ ] typed Write
- [ ] Write disabled by default
- [ ] reconnect
- [ ] stale handle invalidation
- [ ] HTTP status
- [ ] HTTP browse
- [ ] HTTP read
- [ ] HTTP write
- [ ] partial failure
- [ ] hard resource lis
- [ ] no process-value persistence
- [ ] no process-value logging by default
- [ ] unit tests
- [ ] Windows integration test
- [ ] x86/x64 compatibility test
- [ ] long-running soak test
- [ ] at least one public compatibility matrix entry

