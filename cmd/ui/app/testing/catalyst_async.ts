import { ComponentHarness, HarnessQuery } from '@angular/cdk/testing';
import { getHarness as testGetHarness } from './test';

export async function getHarness<T extends ComponentHarness>(query: HarnessQuery<T>): Promise<T> {
  return testGetHarness(query);
}
