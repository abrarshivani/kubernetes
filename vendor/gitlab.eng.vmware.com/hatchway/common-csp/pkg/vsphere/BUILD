load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "cns.go",
        "credentialmanager.go",
        "custom_errors.go",
        "datacenter.go",
        "datastore.go",
        "fcd.go",
        "pbm.go",
        "utils.go",
        "virtualcenter.go",
        "virtualcentermanager.go",
        "virtualmachine.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere",
    importpath = "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/github.com/sirupsen/logrus:go_default_library",
        "//vendor/github.com/vmware/govmomi:go_default_library",
        "//vendor/github.com/vmware/govmomi/find:go_default_library",
        "//vendor/github.com/vmware/govmomi/object:go_default_library",
        "//vendor/github.com/vmware/govmomi/pbm:go_default_library",
        "//vendor/github.com/vmware/govmomi/pbm/methods:go_default_library",
        "//vendor/github.com/vmware/govmomi/pbm/types:go_default_library",
        "//vendor/github.com/vmware/govmomi/property:go_default_library",
        "//vendor/github.com/vmware/govmomi/session:go_default_library",
        "//vendor/github.com/vmware/govmomi/view:go_default_library",
        "//vendor/github.com/vmware/govmomi/vim25:go_default_library",
        "//vendor/github.com/vmware/govmomi/vim25/methods:go_default_library",
        "//vendor/github.com/vmware/govmomi/vim25/mo:go_default_library",
        "//vendor/github.com/vmware/govmomi/vim25/soap:go_default_library",
        "//vendor/github.com/vmware/govmomi/vim25/types:go_default_library",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/cns/methods:go_default_library",
        "//vendor/gitlab.eng.vmware.com/hatchway/common-csp/cns/types:go_default_library",
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
