const YAML_FORMAT_SCENARIO = 'config-yaml-format';

export function parseBrowserSelection(args) {
  let smokeOnly = false;
  let p0Only = false;
  let scenarioId;
  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === '--smoke' && !smokeOnly) smokeOnly = true;
    else if (argument === '--p0' && !p0Only) p0Only = true;
    else if (argument === '--scenario' && scenarioId === undefined) {
      scenarioId = args[++index];
      if (scenarioId !== YAML_FORMAT_SCENARIO) throw new Error(`Unknown or empty built acceptance scenario: ${scenarioId ?? ''}`);
    } else throw new Error(`Unknown or duplicate built acceptance option: ${argument}`);
  }
  if (scenarioId && (smokeOnly || p0Only)) throw new Error('--scenario cannot be combined with --smoke or --p0');
  return { smokeOnly, p0Only, scenarioId };
}
