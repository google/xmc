// Copyright 2025 Google LLC
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
import { Url } from 'xmanager_cloud/experiment_state_server/proto/url.jsonpb_decls';

import { EXPERIMENT_1, WORK_UNIT_1, WORK_UNIT_2 } from '../mock_data';
import { processUrls } from './url_processor';

describe('UrlProcessor', () => {
  it('should process experiment urls and sort by priority', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${XID}',
        displayName: 'Test URL',
        icon: 'link',
        priority: 10,
      },
      {
        url: 'http://test2.com/${XID}',
        displayName: 'Test URL 2',
        icon: 'link',
        priority: 1,
      },
    ];
    const processedUrls = processUrls(urls, EXPERIMENT_1);
    expect(processedUrls.length).toBe(2);
    expect(processedUrls[0].processedUrl).toBe('http://test2.com/1');
    expect(processedUrls[1].processedUrl).toBe('http://test.com/1');
  });

  it('should return undefined processedUrl for missing placeholders', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${XID}',
        displayName: 'Test URL',
        icon: 'link',
        priority: 10,
      },
      {
        url: 'http://test2.com/${WID}',
        displayName: 'Test URL 2',
        icon: 'link',
        priority: 1,
      },
    ];
    const processedUrls = processUrls(urls, EXPERIMENT_1);
    expect(processedUrls.length).toBe(2);
    expect(processedUrls[0].processedUrl).toBeUndefined();
    expect(processedUrls[0].displayName).toBe('Test URL 2');
    expect(processedUrls[1].processedUrl).toBe('http://test.com/1');
    expect(processedUrls[1].displayName).toBe('Test URL');
  });

  it('should process work unit urls', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${XID}/${WID}/${CREATED}',
        displayName: 'Test URL',
        icon: 'link',
        priority: 1,
      },
    ];
    const processedUrls = processUrls(urls, WORK_UNIT_1);
    expect(processedUrls.length).toBe(1);
    expect(processedUrls[0].processedUrl).toBe(`http://test.com/1/1/${WORK_UNIT_1.createTime}`);
  });

  it('should process work unit urls with metadata', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${PROJECT}/${LOCATION}/${CLUSTER}/${RESOURCE_NAME}/${NAMESPACE}/${SERVICE_ACCOUNT}',
        displayName: 'Test URL',
        icon: 'link',
        priority: 1,
      },
    ];
    const processedUrls = processUrls(urls, WORK_UNIT_1);
    expect(processedUrls.length).toBe(1);
    expect(processedUrls[0].processedUrl).toBe(
      `http://test.com/test-project/us-central1/test-cluster/test-resource/test-namespace/test-sa`,
    );
  });

  it('should prefer executingCluster over managementCluster when present', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${PROJECT}/${LOCATION}/${CLUSTER}/${RESOURCE_NAME}/${NAMESPACE}/${SERVICE_ACCOUNT}',
        displayName: 'Test URL',
        icon: 'link',
        priority: 1,
      },
    ];
    const workUnitWithExecutingCluster = {
      ...WORK_UNIT_1,
      currentWorkUnitExecution: {
        ...WORK_UNIT_1.currentWorkUnitExecution,
        metadata: {
          ...WORK_UNIT_1.currentWorkUnitExecution.metadata,
          executingCluster: {
            project: 'worker-project',
            location: 'us-east1',
            clusterName: 'worker-cluster',
          },
        },
      },
    };
    const processedUrls = processUrls(urls, workUnitWithExecutingCluster);
    expect(processedUrls.length).toBe(1);
    expect(processedUrls[0].processedUrl).toBe(
      `http://test.com/worker-project/us-east1/worker-cluster/test-resource/test-namespace/test-sa`,
    );
  });

  it('should return undefined processedUrl for work unit with missing metadata', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${PROJECT}/${LOCATION}/${CLUSTER}/${RESOURCE_NAME}/${NAMESPACE}/${SERVICE_ACCOUNT}',
        displayName: 'Test URL',
        icon: 'link',
        priority: 1,
      },
    ];
    const processedUrls = processUrls(urls, WORK_UNIT_2);
    expect(processedUrls.length).toBe(1);
    expect(processedUrls[0].processedUrl).toBeUndefined();
    expect(processedUrls[0].displayName).toBe('Test URL');
  });

  it('should default to link icon if no icon is provided', () => {
    const urls: Url[] = [
      {
        url: 'http://test.com/${XID}',
        displayName: 'Test URL no icon',
        priority: 1,
      },
      {
        url: 'http://test2.com/${XID}',
        displayName: 'Test URL with icon',
        icon: 'article',
        priority: 2,
      },
      {
        url: 'http://test3.com/${XID}',
        displayName: 'Test URL with empty icon',
        icon: '',
        priority: 3,
      },
    ];
    const processedUrls = processUrls(urls, EXPERIMENT_1);
    expect(processedUrls.length).toBe(3);
    expect(processedUrls[0].icon).toBe('link');
    expect(processedUrls[1].icon).toBe('article');
    expect(processedUrls[2].icon).toBe('link');
  });
});
