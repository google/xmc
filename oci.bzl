def oci_image_for_service(name, binary):
    # Split binary name
    binary_name = binary.split(":")[-1]

    native.genrule(
        name = name + "_tarball",
        srcs = [binary],
        outs = [name + "_tarball.tar"],
        cmd = "python3 $(location //tools:build_oci.py) $< " + binary_name + " " + name + ":latest $@",
        tools = ["//tools:build_oci.py"],
        visibility = ["//visibility:public"],
    )
