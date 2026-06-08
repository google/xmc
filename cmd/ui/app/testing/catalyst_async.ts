import { ComponentHarness, HarnessQuery } from '@angular/cdk/testing';

export async function getHarness<T extends ComponentHarness>(query: HarnessQuery<T>): Promise<T> {
  return (globalThis as any).getHarness(query);
}
