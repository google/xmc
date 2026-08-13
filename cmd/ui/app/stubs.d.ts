// Copyright 2026 Google LLC
declare module 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls' {
  export interface Experiment {
    id?: string;
    name?: string;
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls' {
  export interface WorkUnit {
    id?: string;
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls' {
  export interface ListExperimentsRequest {
    [key: string]: any;
  }
  export interface ListExperimentsResponse {
    experiments?: any[];
    [key: string]: any;
  }
  export interface ListStatusMessagesRequest {
    [key: string]: any;
  }
  export interface ListStatusMessagesResponse {
    statusMessages?: any[];
    [key: string]: any;
  }
  export interface ListArtifactsRequest {
    [key: string]: any;
  }
  export interface ListArtifactsResponse {
    artifacts?: any[];
    [key: string]: any;
  }
  export interface ListWorkUnitsRequest {
    [key: string]: any;
  }
  export interface ListWorkUnitsResponse {
    workUnits?: any[];
    [key: string]: any;
  }
  export interface GetWorkUnitRequest {
    [key: string]: any;
  }
  export interface UpdateExperimentRequest {
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/status.jsonpb_decls' {
  export namespace WorkUnitStatus {
    export type WorkUnitState = string;
  }
  export interface WorkUnitStatus {
    [key: string]: any;
  }
  export namespace ExperimentStatus {
    export interface StateCount {
      [key: string]: any;
    }
    export type ExperimentState = string;
  }
  export interface ExperimentStatus {
    [key: string]: any;
  }
  export interface ReplicatedJobStatus {
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/priority.jsonpb_decls' {
  export type Priority = string;
}
declare module 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls' {
  export namespace StructuredMessage {
    export type Severity = 'WARNING' | 'ERROR' | 'FATAL' | 'INFO' | 'DEBUG' | string;
    export type Source =
      | 'SOURCE_UNSPECIFIED'
      | 'XMANAGER_CLIENT'
      | 'XMANAGER_BACKEND'
      | 'ADAPTER'
      | 'KUBERNETES'
      | 'SCHEDULER'
      | 'APPLICATION'
      | 'USER_DEFINED'
      | string;
  }
  export interface StructuredMessage {
    [key: string]: any;
  }
  export interface QueueInfo {
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls' {
  export namespace Artifact {
    export type DataType = string;
    export type LifecyclePhase = string;
  }
  export interface Artifact {
    [key: string]: any;
  }
  export interface ChartArtifact {
    [key: string]: any;
  }
  export interface CodeSourceArtifact {
    [key: string]: any;
  }
  export interface ManagedXprofArtifact {
    [key: string]: any;
  }
  export interface TextArtifact {
    [key: string]: any;
  }
  export interface UrlArtifact {
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/resources.jsonpb_decls' {
  export interface ResourceSet {
    [key: string]: any;
  }
}
declare module 'xmanager_cloud/experiment_state_server/proto/url.jsonpb_decls' {
  export interface Url {
    priority?: number;
    processedUrl?: string;
    icon?: any;
    [key: string]: any;
  }
}
declare module 'longrunning/operations.jsonpb_decls' {
  export interface Operation {
    [key: string]: any;
  }
}
declare module 'javascript/closure/format/format' {
  export const format: any;
  export function numBytesToString(bytes: number, decimals?: number): string;
}
declare module 'javascript/closure/i18n/messageformat' {
  export class MessageFormat {
    constructor(pattern: string);
    format(args: any): string;
  }
}
declare module 'javascript/closure/i18n/messages' {
  export function declareIcuTemplate(pattern: string, options?: any): any;
}
declare module 'javascript/closure/i18n/numberformat' {
  export class NumberFormat {
    constructor(pattern: string | number);
    format(num: number): string;
    setMaximumFractionDigits(digits: number): void;
    static Format: { COMPACT_SHORT: number; [key: string]: number };
  }
}

declare module 'xmanager_cloud/health_service/proto/api.jsonpb_decls' {
  export interface HealthStatusResponse {
    healthState?: string;
    systemInfo?: any;
    components?: any[];
    healthCheckTime?: any;
    [key: string]: any;
  }
  export interface ListConfigEntriesResponse {
    configGroups?: any[];
    [key: string]: any;
  }
  export type Privilege = string;
  export interface UserInfoResponse {
    userEmail?: string;
    privileges?: Privilege[];
    [key: string]: any;
  }
}

declare module 'xmanager_cloud/api/protos/rod/api.jsonpb_decls' {
  export type WorkUnitState =
    import('xmanager_cloud/experiment_state_server/proto/status.jsonpb_decls').WorkUnitStatus.WorkUnitState;

  export type RangePreset =
    'RANGE_PRESET_UNSPECIFIED' | 'LAST_HOUR' | 'LAST_DAY' | 'LAST_WEEK' | number;

  export interface Resource {
    resourceType?: string;
    count?: number;
  }

  export interface ResourceVelocity {
    resourceType?: string;
    velocity?: number;
  }

  export interface QueueResourceCapacity {
    resourceType?: string;
    totalCapacity?: number;
    freeCapacity?: number;
    freeCapacityWithBorrowing?: number;
  }

  export interface QueueOverview {
    queueName?: string;
    queueCapacity?: QueueResourceCapacity[];
  }

  export interface QueuePredictability {
    pendingResourceRequests?: Resource;
    resourceVelocity?: ResourceVelocity;
    pendingJobs?: number;
    jobVelocity?: number;
    velocityRange?: RangePreset;
  }

  export interface QueueSecondaryData {
    queuePredictability?: QueuePredictability[];
  }

  export interface GetFittingQueuesRequest {
    requirements?: Resource[];
  }

  export interface GetFittingQueuesResponse {
    queues?: QueueOverview[];
  }

  export interface BatchGetQueueSecondaryDataRequest {
    queueNames?: string[];
    range?: RangePreset;
  }

  export interface BatchGetQueueSecondaryDataResponse {
    queuePredictability?: Record<string, QueueSecondaryData>;
  }

  export interface DashboardJob {
    xid?: number;
    workUnitId?: number;
    user?: string;
    priority?: number;
    status?: WorkUnitState;
    timeInQueueSeconds?: number;
    requestedResources?: Resource[];
    positionInQueue?: number;
    createTime?: any;
    tags?: string[];
  }

  export interface GetRunningAndPendingJobsRequest {
    queueName?: string;
    pageSize?: number;
    pageToken?: string;
    orderBy?: string;
  }

  export interface GetRunningAndPendingJobsResponse {
    jobs?: DashboardJob[];
    nextPageToken?: string;
    totalQueueSize?: number;
  }

  export interface GetSubmitQueueRequest {
    queueName?: string;
  }

  export interface GetSubmitQueueResponse {
    submitQueue?: string;
  }
}
