const EXPERIMENT_REGEX = /^experiments\/([^/]+)$/;
const WORK_UNIT_REGEX = /^experiments\/([^/]+)\/workUnits\/([^/]+)$/;
const ARTIFACT_REGEX = /^artifacts\/([^/]+)$/;

/**
 * Parses an experiment name and returns the experiment ID (xid).
 * Expected format: experiments/{xid}
 */
export function parseExperimentName(name: string): string|null {
  const match = name.match(EXPERIMENT_REGEX);
  return match ? match[1] : null;
}

/**
 * Parses a work unit name and returns the experiment ID (xid) and work unit ID
 * (wid).
 * Expected format: experiments/{xid}/workUnits/{wid}
 */
export function parseWorkUnitName(
    name: string,
    ): {xid: string; wid: string}|null {
  const match = name.match(WORK_UNIT_REGEX);
  return match ? {xid: match[1], wid: match[2]} : null;
}

/**
 * Parses an artifact name and returns the artifact ID (aid).
 * Expected format: artifacts/{aid}
 */
export function parseArtifactName(name: string): string|null {
  const match = name.match(ARTIFACT_REGEX);
  return match ? match[1] : null;
}
