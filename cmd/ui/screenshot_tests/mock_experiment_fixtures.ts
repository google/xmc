// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import type {
  Experiment,
  ListArtifactsResponse,
  ListExperimentsResponse,
  ListStatusMessagesResponse,
  ListWorkUnitsResponse,
} from 'app/protos/experiment_state_server';

export const MOCK_EXPERIMENTS_LIST: ListExperimentsResponse = {
  experiments: [
    {
      name: 'experiments/10293841',
      title: 'Gemini 1.5 Pro Fine-Tuning - Run 42',
      creator: 'jdoe@google.com',
      priority: 'HIGH',
      createTime: '2026-06-01T10:00:00Z',
      completeTime: '',
      tags: ['gemini', 'jax', 'v5e-64'],
      notes: 'Distributed pre-training & fine-tuning run on TPU v5e-64 pod slice.',
      status: {
        activeState: 'ACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [
          { state: 'RUNNING', count: 8 },
          { state: 'COMPLETED', count: 2 },
          { state: 'FAILED', count: 1 },
          { state: 'SCHEDULING', count: 1 },
        ],
        structuredStatusMessage: {
          userFacingText:
            'Training step 4500/10000 complete (loss: 0.1824). Checkpoint saved to GCS.',
          severity: 'INFO',
          source: 'APPLICATION',
          createTime: '2026-06-01T10:25:00Z',
        },
      },
      resources: [
        { tpuResources: { type: 'v5e', topology: '4x4x4' } },
        { cpuResources: { cpuMillicores: 128000, memoryBytes: 549755813888 } },
      ],
    },
    {
      name: 'experiments/10293842',
      title: 'MMLU & GSM8K Benchmark Evaluation Suite',
      creator: 'alice@google.com',
      priority: 'NORMAL',
      createTime: '2026-06-01T08:00:00Z',
      completeTime: '2026-06-01T09:30:00Z',
      tags: ['eval', 'benchmark', 'mmlu'],
      notes: 'Standardized accuracy evaluation across reasoning benchmarks.',
      status: {
        activeState: 'INACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [{ state: 'COMPLETED', count: 12 }],
        structuredStatusMessage: {
          userFacingText: 'All 12 evaluation work units completed successfully with 0 errors.',
          severity: 'INFO',
          source: 'APPLICATION',
          createTime: '2026-06-01T09:30:00Z',
        },
      },
      resources: [
        { gpuResources: { type: 'nvidia.com/gpu', count: 8 } },
        { cpuResources: { cpuMillicores: 32000, memoryBytes: 137438953472 } },
      ],
    },
    {
      name: 'experiments/10293843',
      title: 'Quantization & Pruning Ablation Study',
      creator: 'bob@google.com',
      priority: 'LOW',
      createTime: '2026-06-01T07:15:00Z',
      completeTime: '2026-06-01T07:22:00Z',
      tags: ['quantization', 'int8', 'ablation'],
      notes: 'INT8 post-training quantization on vision-language model.',
      status: {
        activeState: 'INACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [
          { state: 'FAILED', count: 2 },
          { state: 'STOPPED', count: 2 },
        ],
        structuredStatusMessage: {
          userFacingText: 'OOM killed: Container exceeded memory limit (128Gi) during calibration.',
          severity: 'ERROR',
          source: 'KUBERNETES',
          createTime: '2026-06-01T07:22:00Z',
        },
      },
      resources: [{ cpuResources: { cpuMillicores: 16000, memoryBytes: 137438953472 } }],
    },
    {
      name: 'experiments/10293844',
      title: 'Llama-3 70B Distributed LoRA Fine-Tuning',
      creator: 'charlie@google.com',
      priority: 'HIGH',
      createTime: '2026-06-01T11:00:00Z',
      completeTime: '',
      tags: ['llama-3', 'lora', 'h100'],
      notes: 'Parameter-efficient fine-tuning on high-throughput GPU cluster.',
      status: {
        activeState: 'ACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [{ state: 'SCHEDULING', count: 4 }],
        structuredStatusMessage: {
          userFacingText:
            'Queue capacity reached; workload queued at position 2 in gpu-cluster-queue.',
          severity: 'WARNING',
          source: 'SCHEDULER',
          createTime: '2026-06-01T11:02:00Z',
        },
      },
      resources: [
        { gpuResources: { type: 'nvidia.com/gpu', count: 32 } },
        { cpuResources: { cpuMillicores: 256000, memoryBytes: 1099511627776 } },
      ],
    },
  ],
  nextPageToken: 'page_token_2',
};

export const MOCK_EXPERIMENT_DETAILS: Experiment = {
  name: 'experiments/10293841',
  title: 'Gemini 1.5 Pro Fine-Tuning - Run 42',
  creator: 'jdoe@google.com',
  priority: 'HIGH',
  createTime: '2026-06-01T10:00:00Z',
  completeTime: '',
  tags: ['gemini', 'jax', 'v5e-64', 'production'],
  notes:
    'Large-scale distributed fine-tuning run with multimodal attention masking on TPU v5e pod slice.',
  status: {
    activeState: 'ACTIVE',
    launchState: 'COMPLETED',
    childStateCounts: [
      { state: 'RUNNING', count: 8 },
      { state: 'COMPLETED', count: 2 },
      { state: 'FAILED', count: 1 },
      { state: 'SCHEDULING', count: 1 },
    ],
    structuredStatusMessage: {
      userFacingText:
        'Training step 4500/10000 in progress. Gradient norm: 0.842, throughput: 1420 tokens/sec.',
      severity: 'INFO',
      source: 'APPLICATION',
      createTime: '2026-06-01T10:25:00Z',
    },
  },
  urls: [
    {
      url: 'https://tensorboard.example.com/experiment/10293841',
      displayName: 'Tensorboard Dashboard',
      icon: 'insights',
      priority: 1,
    },
    {
      url: 'https://console.cloud.google.com/vertex-ai/models',
      displayName: 'Vertex Model Registry',
      icon: 'cloud',
      priority: 2,
    },
    {
      url: 'https://monitoring.xmc.internal/dashboards/tpu-metrics',
      displayName: 'TPU Cluster Metrics',
      icon: 'monitoring',
      priority: 3,
    },
  ],
  resources: [
    { tpuResources: { type: 'v5e', topology: '4x4x4' } },
    { cpuResources: { cpuMillicores: 128000, memoryBytes: 549755813888 } },
  ],
};

export const MOCK_EXPERIMENT_WORK_UNITS: ListWorkUnitsResponse = {
  workUnits: [
    {
      name: 'experiments/10293841/workUnits/1',
      workUnitType: 'STANDARD',
      priority: 'HIGH',
      createTime: '2026-06-01T10:00:00Z',
      tags: ['coordinator', 'rank-0'],
      notes: 'Primary coordinator for parameter synchronization and checkpoint orchestration.',
      urls: [
        {
          url: 'https://console.cloud.google.com/kubernetes/pod/us-central1/tpu-cluster/xmc/coordinator-0',
          displayName: 'Pod Logs',
          icon: 'description',
          priority: 1,
        },
      ],
      currentWorkUnitExecution: {
        createTime: '2026-06-01T10:01:00Z',
        status: {
          state: 'RUNNING',
          userMessage: {
            userFacingText: 'Synchronizing model weights with workers (step 4500)',
            severity: 'INFO',
            source: 'APPLICATION',
          },
          detailedStatus: {
            jobsetStatus: {
              replicatedJobsStatus: [
                { name: 'coordinator-job', active: 1, ready: 1, succeeded: 0, failed: 0 },
              ],
            },
          },
        },
      },
    },
    {
      name: 'experiments/10293841/workUnits/2',
      workUnitType: 'STANDARD',
      priority: 'HIGH',
      createTime: '2026-06-01T10:00:00Z',
      tags: ['worker', 'slice-0'],
      notes: 'Distributed TPU worker slice handling batches 0-31.',
      urls: [
        {
          url: 'https://console.cloud.google.com/kubernetes/pod/us-central1/tpu-cluster/xmc/worker-0',
          displayName: 'Pod Logs',
          icon: 'description',
          priority: 1,
        },
      ],
      currentWorkUnitExecution: {
        createTime: '2026-06-01T10:01:00Z',
        status: {
          state: 'RUNNING',
          detailedStatus: {
            jobsetStatus: {
              replicatedJobsStatus: [
                { name: 'worker-job', active: 8, ready: 8, succeeded: 0, failed: 0 },
              ],
            },
          },
        },
      },
    },
    {
      name: 'experiments/10293841/workUnits/3',
      workUnitType: 'STANDARD',
      priority: 'NORMAL',
      createTime: '2026-06-01T10:00:00Z',
      tags: ['evaluator', 'val-loss'],
      notes: 'Continuous validation loss evaluator.',
      currentWorkUnitExecution: {
        createTime: '2026-06-01T10:01:00Z',
        completeTime: '2026-06-01T10:20:00Z',
        status: {
          state: 'COMPLETED',
          userMessage: {
            userFacingText: 'Validation epoch 2 finished: val_loss=0.231',
            severity: 'INFO',
            source: 'APPLICATION',
          },
        },
      },
    },
    {
      name: 'experiments/10293841/workUnits/4',
      workUnitType: 'AUXILIARY',
      priority: 'LOW',
      createTime: '2026-06-01T10:00:00Z',
      tags: ['profiler', 'xprof'],
      notes: 'XProf memory profiler and trace collector.',
      currentWorkUnitExecution: {
        createTime: '2026-06-01T10:01:00Z',
        completeTime: '2026-06-01T10:05:00Z',
        status: {
          state: 'FAILED',
          systemMessage: {
            userFacingText: 'Trace collection buffer overflow: exceeded max buffer size (2GB)',
            severity: 'ERROR',
            source: 'APPLICATION',
          },
        },
      },
    },
  ],
  nextPageToken: '',
};

export const MOCK_EXPERIMENT_ARTIFACTS: ListArtifactsResponse = {
  artifacts: [
    {
      name: 'artifacts/101',
      parent: 'experiments/10293841',
      lifecyclePhase: 'INPUT',
      tags: ['checkpoint', 'base-weights'],
      notes: 'Pre-trained foundation model weights checkpoint.',
      payload: {
        url: {
          url: 'gs://xmc-checkpoints/gemini-1.5-base/ckpt-100000',
          displayName: 'Base Foundation Checkpoint',
          icon: 'inventory_2',
        },
      },
    },
    {
      name: 'artifacts/102',
      parent: 'experiments/10293841',
      lifecyclePhase: 'INPUT',
      tags: ['config', 'hparams'],
      notes: 'Training hyperparameters configuration YAML.',
      payload: {
        text: {
          title: 'Hyperparameters Configuration',
          text: 'learning_rate: 0.0001\nwarmup_steps: 500\nweight_decay: 0.01\noptimizer: adamw\nprecision: bfloat16',
        },
      },
    },
    {
      name: 'artifacts/103',
      parent: 'experiments/10293841',
      lifecyclePhase: 'EXECUTION',
      tags: ['metrics', 'live'],
      notes: 'Live scalar training metrics and telemetry stream.',
      payload: {
        url: {
          url: 'https://tensorboard.example.com/experiment/10293841/scalars',
          displayName: 'Live Training Scalars',
          icon: 'trending_up',
        },
      },
    },
    {
      name: 'artifacts/104',
      parent: 'experiments/10293841',
      lifecyclePhase: 'OUTPUT',
      tags: ['checkpoint', 'final-lora'],
      notes: 'Fine-tuned LoRA adapter weights and tokenizer configuration.',
      payload: {
        url: {
          url: 'gs://xmc-checkpoints/gemini-1.5-pro-run42/adapter_weights.bin',
          displayName: 'Fine-Tuned Adapter Weights',
          icon: 'download',
        },
      },
    },
    {
      name: 'artifacts/105',
      parent: 'experiments/10293841',
      lifecyclePhase: 'OUTPUT',
      tags: ['code', 'eval'],
      notes: 'Evaluation harness reproduction snippet.',
      payload: {
        codeSource: {
          title: 'Evaluation Script Reproduction',
          description:
            'Python snippet to evaluate the adapter weights on custom benchmark dataset.',
          codeBlock:
            'from xmc.eval import Evaluator\nevaluator = Evaluator.load("gs://xmc-checkpoints/gemini-1.5-pro-run42")\nresults = evaluator.run_benchmark("mmlu_pro")',
          codeUrl: 'https://github.com/google/xmc',
        },
      },
    },
  ],
};

export const MOCK_EXPERIMENT_STATUS_MESSAGES: ListStatusMessagesResponse = {
  statusMessages: [
    {
      name: 'experiments/10293841/statusMessages/1',
      createTime: '2026-06-01T10:00:05Z',
      content: {
        userFacingText: 'Experiment manifest created and validated by XMC state engine.',
        severity: 'INFO',
        source: 'XMANAGER_BACKEND',
        createTime: '2026-06-01T10:00:05Z',
      },
    },
    {
      name: 'experiments/10293841/statusMessages/2',
      createTime: '2026-06-01T10:00:45Z',
      content: {
        userFacingText: 'Kueue admitted workload to gpu-cluster-queue (priority 100).',
        severity: 'INFO',
        source: 'SCHEDULER',
        createTime: '2026-06-01T10:00:45Z',
      },
    },
    {
      name: 'experiments/10293841/statusMessages/3',
      createTime: '2026-06-01T10:05:00Z',
      content: {
        userFacingText: 'WorkUnit 4 profiler warning: trace buffer overflow reached.',
        severity: 'WARNING',
        source: 'APPLICATION',
        createTime: '2026-06-01T10:05:00Z',
      },
    },
    {
      name: 'experiments/10293841/statusMessages/4',
      createTime: '2026-06-01T10:25:00Z',
      content: {
        userFacingText:
          'Training step 4500/10000 in progress. Gradient norm: 0.842, throughput: 1420 tokens/sec.',
        severity: 'INFO',
        source: 'APPLICATION',
        createTime: '2026-06-01T10:25:00Z',
      },
    },
  ],
};
