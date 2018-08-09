load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "enums.go",
        "if.go",
        "types.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types",
    importpath = "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types",
    visibility = ["//visibility:public"],
    deps = ["//vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere:go_default_library"],
)

filegroup(
    name = "package-srcs",
    srcs = glob(["**"]),
    tags = ["automanaged"],
    visibility = ["//visibility:private"],
)

filegroup(
    name = "all-srcs",
    srcs = [":package-srcs"],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
