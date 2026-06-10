// Custom local stub for NumberFormat
export class NumberFormat {
  private maxFractionDigits = 1;
  constructor(pattern: any) {}
  setMaximumFractionDigits(digits: number): void {
    this.maxFractionDigits = digits;
  }
  format(num: number): string {
    return num.toLocaleString('en-US', {
      maximumFractionDigits: this.maxFractionDigits,
    });
  }
  static Format = {
    COMPACT_SHORT: 1,
  };
}
