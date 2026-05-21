// Mock document for elkjs in Node environment
const mockElement = {
  appendChild: () => mockElement,
  setAttribute: () => {},
};

(global as any).document = {
  createElement: () => mockElement,
  head: mockElement,
  body: mockElement,
};

// Mock ELK constructor for raw_js_library loader
class MockELK {
  layout(graph: any) {
    return Promise.resolve(graph);
  }
}

(global as any).ELK = MockELK;
