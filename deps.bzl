"""Bazel dependencies for Go modules."""

load("@bazel_gazelle//:deps.bzl", "go_repository")

def go_dependencies():
    """Declare all Go module dependencies for Bazel build."""
    go_repository(
        name = "com_github_alecthomas_kingpin_v2",
        importpath = "github.com/alecthomas/kingpin/v2",
        sum = "h1:f48lwail6p8zpO1bC4TxtqACaGqHYA22qkHjHpqDjYY=",
        version = "v2.4.0",
    )
    go_repository(
        name = "com_github_alecthomas_units",
        importpath = "github.com/alecthomas/units",
        sum = "h1:mimo19zliBX/vSQ6PWWSL9lK8qwHozUj03+zLoEB8O0=",
        version = "v0.0.0-20240927000941-0f3dac36c52b",
    )
    go_repository(
        name = "com_github_beorn7_perks",
        importpath = "github.com/beorn7/perks",
        sum = "h1:VlbKKnNfV8bJzeqoa4cOKqO6bYr3WgKZxO8Z16+hsOM=",
        version = "v1.0.1",
    )
    go_repository(
        name = "com_github_cespare_xxhash_v2",
        importpath = "github.com/cespare/xxhash/v2",
        sum = "h1:UL815xU9SqsFlibzuggzjXhog7bL6oX9BbNZnL2UFvs=",
        version = "v2.3.0",
    )
    go_repository(
        name = "com_github_creack_pty",
        importpath = "github.com/creack/pty",
        sum = "h1:bJrF4RRfyJnbTJqzRLHzcGaZK1NeM5kTC9jGgovnR1s=",
        version = "v1.1.24",
    )
    go_repository(
        name = "com_github_davecgh_go_spew",
        importpath = "github.com/davecgh/go-spew",
        sum = "h1:vj9j/u1bqnvCEfJOwUhtlOARqs3+rkHYY13jYWTU97c=",
        version = "v1.1.1",
    )
    go_repository(
        name = "com_github_dgraph_io_badger_v4",
        importpath = "github.com/dgraph-io/badger/v4",
        sum = "h1:IQqMPVGLNCQr1b4Mu8lHkYm/xyqFRsyKaFEtyLi9CCQ=",
        version = "v4.9.6",
    )
    go_repository(
        name = "com_github_dgraph_io_ristretto_v2",
        importpath = "github.com/dgraph-io/ristretto/v2",
        sum = "h1:bkY3XzJcXoMuELV8F+vS8kzNgicwQFAaGINAEJdWGOM=",
        version = "v2.2.0",
    )
    go_repository(
        name = "com_github_dgryski_go_farm",
        importpath = "github.com/dgryski/go-farm",
        sum = "h1:aIftn67I1fkbMa512G+w+Pxci9hJPB8oMnkcP3iZF38=",
        version = "v0.0.0-20240924180020-3414d57e47da",
    )
    go_repository(
        name = "com_github_dustin_go_humanize",
        importpath = "github.com/dustin/go-humanize",
        sum = "h1:dbKTrvD0klcbBV/h4AWJdMuZogJACoMlvWIWZ5b2xWg=",
        version = "v1.1.0",
    )
    go_repository(
        name = "com_github_go_logr_logr",
        importpath = "github.com/go-logr/logr",
        sum = "h1:CjnDlHq8ikf6E492q6eKboGOC0T8CDaOvkHCIg8idEI=",
        version = "v1.4.3",
    )
    go_repository(
        name = "com_github_go_logr_stdr",
        importpath = "github.com/go-logr/stdr",
        sum = "h1:hSWxHoqTgW2S2qGc0LTAI563KZ5YKYRhT3MFKZMbjag=",
        version = "v1.2.2",
    )
    go_repository(
        name = "com_github_goccy_go_yaml",
        importpath = "github.com/goccy/go-yaml",
        sum = "h1:PmFC1S6h8ljIz6gMRBopkjP1TVT7xuwrButHID66PoM=",
        version = "v1.19.2",
    )
    go_repository(
        name = "com_github_golang_jwt_jwt_v5",
        importpath = "github.com/golang-jwt/jwt/v5",
        sum = "h1:kYf81DTWFe7t+1VvL7eS+jKFVWaUnK9cB1qbwn63YCY=",
        version = "v5.3.1",
    )
    go_repository(
        name = "com_github_golang_protobuf",
        importpath = "github.com/golang/protobuf",
        sum = "h1:LUVKkCeviFUMKqHa4tXIIij/lbhnMbP7Fn5wKdKkRh4=",
        version = "v1.5.0",
    )
    go_repository(
        name = "com_github_google_flatbuffers",
        importpath = "github.com/google/flatbuffers",
        sum = "h1:F3vclr7C3HpB1k9mxCGRMXq6FdUalZ6H/pNX4FP1v0Q=",
        version = "v25.2.10+incompatible",
    )
    go_repository(
        name = "com_github_google_go_cmp",
        importpath = "github.com/google/go-cmp",
        sum = "h1:wk8382ETsv4JYUZwIsn6YpYiWiBsYLSJiTsyBybVuN8=",
        version = "v0.7.0",
    )
    go_repository(
        name = "com_github_google_pprof",
        importpath = "github.com/google/pprof",
        sum = "h1:PlNWJRxu5APSV0pWqfIsZ0qt9OOGHSWfZQ4zvx+WgWs=",
        version = "v0.0.0-20261016150608-1facf8f91b50",
    )
    go_repository(
        name = "com_github_google_uuid",
        importpath = "github.com/google/uuid",
        sum = "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=",
        version = "v1.6.0",
    )
    go_repository(
        name = "com_github_gorilla_websocket",
        importpath = "github.com/gorilla/websocket",
        sum = "h1:saDtZ6Pbx/0u+bgYQ3q96pZgCzfhKXGPqt7kZ72aNNg=",
        version = "v1.5.3",
    )
    go_repository(
        name = "com_github_hashicorp_golang_lru_v2",
        importpath = "github.com/hashicorp/golang-lru/v2",
        sum = "h1:a+bsQ5rvGLjzHuww6tVxozPZFVghXaHOwFs4luLUK2k=",
        version = "v2.0.7",
    )
    go_repository(
        name = "com_github_inconshreveable_mousetrap",
        importpath = "github.com/inconshreveable/mousetrap",
        sum = "h1:wN+x4NVGpMsO7ErUn/mUI3vEoE6Jt13X2s0bqwp9tc8=",
        version = "v1.1.0",
    )
    go_repository(
        name = "com_github_jpillora_backoff",
        importpath = "github.com/jpillora/backoff",
        sum = "h1:uvFg412JmmHBHw7iwprIxkPMI+sGQ4kzOWsMeHnm2EA=",
        version = "v1.0.0",
    )
    go_repository(
        name = "com_github_json_iterator_go",
        importpath = "github.com/json-iterator/go",
        sum = "h1:PV8peI4a0ysnczrg+LtxykD8LfKY9ML6u2jnxaEnrnM=",
        version = "v1.1.12",
    )
    go_repository(
        name = "com_github_julienschmidt_httprouter",
        importpath = "github.com/julienschmidt/httprouter",
        sum = "h1:U0609e9tgbseu3rBINet9P48AI/D3oJs4dN7jwJOQ1U=",
        version = "v1.3.0",
    )
    go_repository(
        name = "com_github_klauspost_compress",
        importpath = "github.com/klauspost/compress",
        sum = "h1:T7kKElXUMXrUJ2E9QhQhxFtcK5rPyLdsGZvdbLMPdiQ=",
        version = "v1.20.1",
    )
    go_repository(
        name = "com_github_kr_pretty",
        importpath = "github.com/kr/pretty",
        sum = "h1:flRD4NNwYAUpkphVc1HcthR4KEIFJ65n8Mw5qdRn3LE=",
        version = "v0.3.1",
    )
    go_repository(
        name = "com_github_kr_text",
        importpath = "github.com/kr/text",
        sum = "h1:5Nx0Ya0ZqY2ygV366QzturHI13Jq95ApcVaJBhpS+AY=",
        version = "v0.2.0",
    )
    go_repository(
        name = "com_github_kylelemons_godebug",
        importpath = "github.com/kylelemons/godebug",
        sum = "h1:RPNrshWIDI6G2gRW9EHilWtl7Z6Sb1BR0xunSBf0SNc=",
        version = "v1.1.0",
    )
    go_repository(
        name = "com_github_mattn_go_isatty",
        importpath = "github.com/mattn/go-isatty",
        sum = "h1:tGZZoVgT/KiqK1c8ocVLeDS8BSWMRd47J3Lbz7vsReI=",
        version = "v0.0.24",
    )
    go_repository(
        name = "com_github_modern_go_concurrent",
        importpath = "github.com/modern-go/concurrent",
        sum = "h1:TRLaZ9cD/w8PVh93nsPXa1VrQ6jlwL5oN8l14QlcNfg=",
        version = "v0.0.0-20180306012644-bacd9c7ef1dd",
    )
    go_repository(
        name = "com_github_modern_go_reflect2",
        importpath = "github.com/modern-go/reflect2",
        sum = "h1:xBagoLtFs94CBntxluKeaWgTMpvLxC4ur3nMaC9Gz0M=",
        version = "v1.0.2",
    )
    go_repository(
        name = "com_github_munnerz_goautoneg",
        importpath = "github.com/munnerz/goautoneg",
        sum = "h1:C3w9PqII01/Oq1c1nUAm88MOHcQC9l5mIlSMApZMrHA=",
        version = "v0.0.0-20191010083416-a7dc8b61c822",
    )
    go_repository(
        name = "com_github_mwitkow_go_conntrack",
        importpath = "github.com/mwitkow/go-conntrack",
        sum = "h1:KUppIJq7/+SVif2QVs3tOP0zanoHgBEVAwHxUSIzRqU=",
        version = "v0.0.0-20190716064945-2f068394615f",
    )
    go_repository(
        name = "com_github_ncruces_go_strftime",
        importpath = "github.com/ncruces/go-strftime",
        sum = "h1:vtPmPWMkb539RGV9WpWT9EwJGvrOCKvMNI4obWL47Hk=",
        version = "v1.1.0",
    )
    go_repository(
        name = "com_github_pmezard_go_difflib",
        importpath = "github.com/pmezard/go-difflib",
        sum = "h1:4DBwDE0NGyQoBHbLQYPwSUPoCMWR5BEzIk/f1lZbAQM=",
        version = "v1.0.0",
    )
    go_repository(
        name = "com_github_prometheus_client_golang",
        importpath = "github.com/prometheus/client_golang",
        sum = "h1:cDtW1TcFsMi1RJc8khwCKB4VhzwV04aKqbYtNOXuiK0=",
        version = "v1.25.0",
    )
    go_repository(
        name = "com_github_prometheus_client_model",
        importpath = "github.com/prometheus/client_model",
        sum = "h1:O0jaTVAYNxTHYInEPFJt5I3+sN8zqBtVMPTB1qyxiEo=",
        version = "v0.6.3",
    )
    go_repository(
        name = "com_github_prometheus_common",
        importpath = "github.com/prometheus/common",
        sum = "h1:tAYsE+sPJxIncDAobm4H5aQjmox9ZxEIIqPbiffa8G4=",
        version = "v0.72.0",
    )
    go_repository(
        name = "com_github_prometheus_procfs",
        importpath = "github.com/prometheus/procfs",
        sum = "h1:6q9+/JL9IKAPbCmBrv9n5O5Ty3NKnciV5X7YGw0oics=",
        version = "v0.22.0",
    )
    go_repository(
        name = "com_github_remyoudompheng_bigfft",
        importpath = "github.com/remyoudompheng/bigfft",
        sum = "h1:W09IVJc94icq4NjY3clb7Lk8O1qJ8BdBEF8z0ibU0rE=",
        version = "v0.0.0-20230129092748-24d4a6f8daec",
    )
    go_repository(
        name = "com_github_rogpeppe_go_internal",
        importpath = "github.com/rogpeppe/go-internal",
        sum = "h1:UQB4HGPB6osV0SQTLymcB4TgvyWu6ZyliaW0tI/otEQ=",
        version = "v1.14.1",
    )
    go_repository(
        name = "com_github_spf13_cobra",
        importpath = "github.com/spf13/cobra",
        sum = "h1:CXSaggrXdbHK9CF+8ywj8Amf7PBRmPCOJugH954Nnlo=",
        version = "v1.9.1",
    )
    go_repository(
        name = "com_github_spf13_pflag",
        importpath = "github.com/spf13/pflag",
        sum = "h1:jFzHGLGAlb3ruxLB8MhbI6A8+AQX/2eW4qeyNZXNp2o=",
        version = "v1.0.6",
    )
    go_repository(
        name = "com_github_stretchr_objx",
        importpath = "github.com/stretchr/objx",
        sum = "h1:jmXUvGomnU1o3W/V5h2VEradbpJDwGrzugQQvL0POH4=",
        version = "v0.5.3",
    )
    go_repository(
        name = "com_github_stretchr_testify",
        importpath = "github.com/stretchr/testify",
        sum = "h1:EuwCh5fleGS7H32xRwO3wRGT7DxrDhLAT6FF8MpWDWE=",
        version = "v1.12.1",
    )
    go_repository(
        name = "com_github_xhit_go_str2duration_v2",
        importpath = "github.com/xhit/go-str2duration/v2",
        sum = "h1:lxklc02Drh6ynqX+DdPyp5pCKLUQpRT8bp8Ydu2Bstc=",
        version = "v2.1.0",
    )
    go_repository(
        name = "in_gopkg_check_v1",
        importpath = "gopkg.in/check.v1",
        sum = "h1:Hei/4ADfdWqJk1ZMxUNpqntNwaWcugrBjAiHlqqRiVk=",
        version = "v1.0.0-20201130134442-10cb98267c6c",
    )
    go_repository(
        name = "in_gopkg_yaml_v3",
        importpath = "gopkg.in/yaml.v3",
        sum = "h1:fxVm/GzAzEWqLHuvctI91KS9hhNmmWOoWu0XTYJS7CA=",
        version = "v3.0.1",
    )
    go_repository(
        name = "in_yaml_go_yaml_v2",
        importpath = "go.yaml.in/yaml/v2",
        sum = "h1:tuyd0P+2Ont/d6e2rl3be67goVK4R6deVxCUX5vyPaQ=",
        version = "v2.4.4",
    )
    go_repository(
        name = "in_yaml_go_yaml_v3",
        importpath = "go.yaml.in/yaml/v3",
        sum = "h1:N6y/pJk8buWs9NY5ERU2HSMfm+IuD/OtfdAnq6kESPw=",
        version = "v3.0.5",
    )
    go_repository(
        name = "io_opentelemetry_go_auto_sdk",
        importpath = "go.opentelemetry.io/auto/sdk",
        sum = "h1:jXsnJ4Lmnqd11kwkBV2LgLoFMZKizbCi5fNZ/ipaZ64=",
        version = "v1.2.1",
    )
    go_repository(
        name = "io_opentelemetry_go_contrib_zpages",
        importpath = "go.opentelemetry.io/contrib/zpages",
        sum = "h1:fFAdraKB5z4v8HWwQWoKFZojN+OdsEtVDKsXFyB5+ro=",
        version = "v0.66.0",
    )
    go_repository(
        name = "io_opentelemetry_go_otel",
        importpath = "go.opentelemetry.io/otel",
        sum = "h1:YlEwVsGAlCvczDILpUXpIpPSL/VPugt7zHThEMLce1c=",
        version = "v1.41.0",
    )
    go_repository(
        name = "io_opentelemetry_go_otel_metric",
        importpath = "go.opentelemetry.io/otel/metric",
        sum = "h1:rFnDcs4gRzBcsO9tS8LCpgR0dxg4aaxWlJxCno7JlTQ=",
        version = "v1.41.0",
    )
    go_repository(
        name = "io_opentelemetry_go_otel_sdk",
        importpath = "go.opentelemetry.io/otel/sdk",
        sum = "h1:YPIEXKmiAwkGl3Gu1huk1aYWwtpRLeskpV+wPisxBp8=",
        version = "v1.41.0",
    )
    go_repository(
        name = "io_opentelemetry_go_otel_trace",
        importpath = "go.opentelemetry.io/otel/trace",
        sum = "h1:Vbk2co6bhj8L59ZJ6/xFTskY+tGAbOnCtQGVVa9TIN0=",
        version = "v1.41.0",
    )
    go_repository(
        name = "org_golang_google_protobuf",
        importpath = "google.golang.org/protobuf",
        sum = "h1:pJOKDDOyeXErUroCihFAd5LQuwXBSpVnKGrj5o/fwxc=",
        version = "v1.36.12",
    )
    go_repository(
        name = "org_golang_x_mod",
        importpath = "golang.org/x/mod",
        sum = "h1:ICeL3t2L30ljSPHHxDnR4fqVcBt/zR2zJAb9juavmQg=",
        version = "v0.42.0",
    )
    go_repository(
        name = "org_golang_x_net",
        importpath = "golang.org/x/net",
        sum = "h1:5zfYln+w5XCxwrnMMJPufRgNoXEaGxl0wo5GqPXyues=",
        version = "v0.59.0",
    )
    go_repository(
        name = "org_golang_x_oauth2",
        importpath = "golang.org/x/oauth2",
        sum = "h1:JUlcxA8oAtauLfiH8FX2/FkAWHAdi0QtGCGc+hofE98=",
        version = "v0.37.0",
    )
    go_repository(
        name = "org_golang_x_sync",
        importpath = "golang.org/x/sync",
        sum = "h1:iqiDrGoDgyuvAKrhLQthUEzU/R378N78gXbBjxed7oA=",
        version = "v0.24.0",
    )
    go_repository(
        name = "org_golang_x_sys",
        importpath = "golang.org/x/sys",
        sum = "h1:XbzkgYJHdqh/8m2Uu0W/dQv8nktxx4BFHp1M0gROTXA=",
        version = "v0.49.0",
    )
    go_repository(
        name = "org_golang_x_text",
        importpath = "golang.org/x/text",
        sum = "h1:JbOZXgfeCPU9gacVtYliJqOhD+zhrEqK4LfdpmlUZqI=",
        version = "v0.42.0",
    )
    go_repository(
        name = "org_golang_x_time",
        importpath = "golang.org/x/time",
        sum = "h1:vMb6ptszcQMkcwiRTAuNNU50gom6++Q/6gY2hDM6VDE=",
        version = "v0.16.0",
    )
    go_repository(
        name = "org_golang_x_tools",
        importpath = "golang.org/x/tools",
        sum = "h1:ryxg4U63oaUYk7uOLTF37n43f4qgtGvqV4Ioit1BaEs=",
        version = "v0.52.0",
    )
    go_repository(
        name = "org_modernc_cc_v4",
        importpath = "modernc.org/cc/v4",
        sum = "h1:aDq8gIX1X7+VFgO/JYYji6pLWL2H4tv3g8WatT3KCys=",
        version = "v4.30.3",
    )
    go_repository(
        name = "org_modernc_ccgo_v4",
        importpath = "modernc.org/ccgo/v4",
        sum = "h1:BAuJCUtIHIxFIYaFhqXIw5YaoMBlNCUlFovejHfMip4=",
        version = "v4.37.0",
    )
    go_repository(
        name = "org_modernc_fileutil",
        importpath = "modernc.org/fileutil",
        sum = "h1:j6ZzNTftVS054gi281TyLjHPp6CPHr2KCxEXjEbD6SM=",
        version = "v1.4.0",
    )
    go_repository(
        name = "org_modernc_gc_v2",
        importpath = "modernc.org/gc/v2",
        sum = "h1:nyqdV8q46KvTpZlsw66kWqwXRHdjIlJOhG6kxiV/9xI=",
        version = "v2.6.5",
    )
    go_repository(
        name = "org_modernc_gc_v3",
        importpath = "modernc.org/gc/v3",
        sum = "h1:/Y3LGDMQaoYf55PebP1qsZ5FDGgAZyG1RYKEHeQzh1o=",
        version = "v3.2.0",
    )
    go_repository(
        name = "org_modernc_goabi0",
        importpath = "modernc.org/goabi0",
        sum = "h1:HvEowk7LxcPd0eq6mVOAEMai46V+i7Jrj13t4AzuNks=",
        version = "v0.2.0",
    )
    go_repository(
        name = "org_modernc_libc",
        importpath = "modernc.org/libc",
        sum = "h1:9Dvvv++bAMSkSuBzuOFAatbTV5OIoq6Y6FohmD3e7q8=",
        version = "v1.79.0",
    )
    go_repository(
        name = "org_modernc_mathutil",
        importpath = "modernc.org/mathutil",
        sum = "h1:GCZVGXdaN8gTqB1Mf/usp1Y/hSqgI2vAGGP4jZMCxOU=",
        version = "v1.7.1",
    )
    go_repository(
        name = "org_modernc_memory",
        importpath = "modernc.org/memory",
        sum = "h1:nFMiWrpStgZczNl6XI9GnIk/rWhYIyHGUaR04pGbp9g=",
        version = "v1.12.1",
    )
    go_repository(
        name = "org_modernc_opt",
        importpath = "modernc.org/opt",
        sum = "h1:tGyef5ApycA7FSEOMraay9SaTk5zmbx7Tu+cJs4QKZg=",
        version = "v0.2.0",
    )
    go_repository(
        name = "org_modernc_sortutil",
        importpath = "modernc.org/sortutil",
        sum = "h1:+xyoGf15mM3NMlPDnFqrteY07klSFxLElE2PVuWIJ7w=",
        version = "v1.2.1",
    )
    go_repository(
        name = "org_modernc_sqlite",
        importpath = "modernc.org/sqlite",
        sum = "h1:TBEE49wz7VlxS+qXRXMQiPUZYOXHJ4ku83daYv1fe04=",
        version = "v1.61.1",
    )
    go_repository(
        name = "org_modernc_strutil",
        importpath = "modernc.org/strutil",
        sum = "h1:UneZBkQA+DX2Rp35KcM69cSsNES9ly8mQWD71HKlOA0=",
        version = "v1.2.1",
    )
    go_repository(
        name = "org_modernc_token",
        importpath = "modernc.org/token",
        sum = "h1:Xl7Ap9dKaEs5kLoOQeQmPWevfnk/DM5qcLcYlA8ys6Y=",
        version = "v1.1.0",
    )
    go_repository(
        name = "org_uber_go_goleak",
        importpath = "go.uber.org/goleak",
        sum = "h1:2K3zAYmnTNqV73imy9J1T3WC+gmCePx2hEGkimedGto=",
        version = "v1.3.0",
    )
