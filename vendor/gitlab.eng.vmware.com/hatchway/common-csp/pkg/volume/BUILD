load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "manager.go",
        "util.go",
    ],
    importpath = "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/github.com/sirupsen/logrus:go_default_library",
        "//vendor/github.com/vmware/govmomi/object:go_default_library",
        "//vendor/github.com/vmware/govmomi/vim25/types:go_default_library",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/cns/types:go_default_library",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/node:go_default_library",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types:go_default_library",
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
    srcs = [
        ":package-srcs",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types:all-srcs",
    ],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
