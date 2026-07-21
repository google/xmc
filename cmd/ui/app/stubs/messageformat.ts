// Custom local stub for MessageFormat
export class MessageFormat {
  constructor(private pattern: string) {}
  format(args: any): string {
    const numCores = args['NUM_CORES'] || 0;
    const formattedCores = args['FORMATTED_CORES'] || '0';
    if (numCores === 1) {
      return `${formattedCores} core`;
    }
    return `${formattedCores} cores`;
  }
}
