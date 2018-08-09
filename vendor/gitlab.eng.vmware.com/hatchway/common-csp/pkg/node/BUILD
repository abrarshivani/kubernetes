load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "cache.go",
        "manager.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/node",
    importpath = "gitlab.eng.vmware.com/hatchway/common-csp/pkg/node",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/github.com/sirupsen/logrus:go_default_library",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere:go_default_library",
    ],
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
