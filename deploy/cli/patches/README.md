# Vendored xmanager (xm-cloud) patches

`install_xmanager.py` clones the public xmanager repo
(`github.com/google-deepmind/xmanager`, branch `xm-cloud`) and builds the
`xmanager_cloud` wheel. These patches carry local fixes that are not yet
upstream in `xm-cloud`. The installer applies every `*.patch` in this directory
(sorted) to the freshly cloned tree with `git apply -p1` before the wheel is
built.

## 0001-mtc-tpu-support.patch

Generated from `xm-cloud @ 7d9803e`. Adds MTC (managed XMC) TPU support:

- `xmanager/xm/resources.py`: add `ResourceType.architecture()` (and the
  resource-type → `Architecture` map) so `_validate_architecture_and_accelerator`
  succeeds for accelerator (TPU/GPU) jobs instead of raising `AttributeError`.
- `xmanager/xm_cloud/experiment.py`: add `_tpu_type_to_gke_accelerator()` and
  rewrite the TPU branch of `_convert_xm_job_to_ess_job` so it emits the GKE
  accelerator string (e.g. `tpu-v6e-slice`, `tpu-v5p-slice`) plus
  `topology.name`, instead of the raw enum name + count. A topology is now
  required for TPU jobs.
