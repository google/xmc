// Mock jasmine module that exports the global jasmine object
export const jasmine = (globalThis as any).jasmine;
export default jasmine;
