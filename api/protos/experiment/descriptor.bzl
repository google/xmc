# Custom rule to merge transitive descriptors
load("@rules_proto//proto:defs.bzl", "ProtoInfo")

def _merge_descriptors_impl(ctx):
    transitive_descriptor_sets = []
    for dep in ctx.attr.deps:
        if ProtoInfo in dep:
            transitive_descriptor_sets.append(dep[ProtoInfo].transitive_descriptor_sets)
    
    # transitive_descriptor_sets is a list of depsets.
    # We merge them into one depset.
    merged_depset = depset(transitive = transitive_descriptor_sets)
    files = merged_depset.to_list()
    
    output = ctx.outputs.out
    
    # Concatenate all descriptor sets.
    # Serialized FileDescriptorSet messages can be concatenated to merge them.
    ctx.actions.run_shell(
        inputs = files,
        outputs = [output],
        command = "cat " + " ".join([f.path for f in files]) + " > " + output.path,
    )
    return [DefaultInfo(files = depset([output]))]

merge_descriptors = rule(
    implementation = _merge_descriptors_impl,
    attrs = {
        "deps": attr.label_list(providers = [ProtoInfo]),
        "out": attr.output(mandatory = True),
    },
)
