import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
import AjvModule from '../web/node_modules/@redocly/ajv/dist/2020.js';

const Ajv = AjvModule.default || AjvModule;
const manifest = JSON.parse(await readFile('api/mcp-tools.json', 'utf8'));
const specification = JSON.parse(await readFile('api/openapi.json', 'utf8'));
const documentation = await readFile('docs/mcp.md', 'utf8');
const names = manifest.tools.map(tool => tool.name);
assert.equal(names.length, 45);
assert.equal(new Set(names).size, 45);
assert.deepEqual(names, [...names].sort());
assert.deepEqual(manifest.protocols, ['2026-07-28', '2025-11-25']);
const documented = [...documentation.matchAll(/^\| `(qag_[a-z_]+)` \|/gm)].map(match => match[1]).sort();
assert.deepEqual(documented, names, 'documentation/registry drift');
const mapping = {
  exportTestCase: 'qag_test_case_export', getHealth: 'qag_status', getDemo: 'qag_demo_get',
  listFolders: 'qag_folders_list', createFolder: 'qag_folder_create', updateFolder: 'qag_folder_update',
  listTestCases: 'qag_test_cases_list', createTestCase: 'qag_test_case_create', getTestCase: 'qag_test_case_get', saveTestCase: 'qag_test_case_save', archiveTestCase: 'qag_test_case_update',
  uploadExpectedImage: 'qag_image_upload', readExpectedImage: 'qag_image_get',
  listEnvironments: 'qag_environments_list', createEnvironment: 'qag_environment_create', saveEnvironment: 'qag_environment_update', saveVariables: 'qag_variables_set',
  listResources: 'qag_resources_list', createResource: 'qag_resource_create', saveResource: 'qag_resource_update', uploadProtoBundle: 'qag_proto_bundle_upload',
  listRuns: 'qag_runs_list', startRun: 'qag_run_start', getRun: 'qag_run_get', updateRunStep: 'qag_run_step_update', passAllRunSteps: 'qag_run_steps_pass_all', finishRun: 'qag_run_finish', cancelRun: 'qag_run_cancel', saveRun: 'qag_run_save', deleteRun: 'qag_run_delete',
  startBatch: 'qag_batch_start', startEditorAttempt: 'qag_editor_attempt_start', startRunAttempt: 'qag_run_attempt_start', getAttempt: 'qag_attempt_get', cancelAttempt: 'qag_attempt_cancel',
  createVariant: 'qag_variant_create', saveVariant: 'qag_variant_update', startVariantAttempt: 'qag_variant_attempt_start',
  getRunReport: 'qag_report_prepare', copyRunReport: 'qag_report_copy',
};
const operations = Object.values(specification.paths).flatMap(path => Object.values(path).map(operation => operation.operationId)).filter(id => id !== 'getSession').sort();
assert.deepEqual(operations, Object.keys(mapping).sort(), 'new UI domain operation needs an MCP mapping');
for (const name of Object.values(mapping)) assert(names.includes(name), `missing ${name}`);
const validators = new Map();
for (const tool of manifest.tools) {
  assert(tool.description && tool.annotations && tool.inputSchema && tool.outputSchema, tool.name);
  const ajv = new Ajv({ strict: false, allErrors: true });
  assert(ajv.validateSchema(tool.inputSchema), `${tool.name} input schema`);
  assert(ajv.validateSchema(tool.outputSchema), `${tool.name} output schema`);
  validators.set(tool.name, ajv.compile(tool.inputSchema));
  ajv.compile(tool.outputSchema);
  assert.equal(tool.inputSchema.additionalProperties, false, `${tool.name} root must be strict`);
}
assert(!validators.get('qag_status')({ unexpected: 'sentinel' }));
const resourceWrite = {environmentId:'00000000-0000-0000-0000-000000000001',name:'TARGET',kind:'http',config:{baseUrl:'http://localhost',headers:[],tls:{mode:'plaintext'},variables:{' ':{mode:'invalid',source:123},GOOD:{mode:'literal',value:''}}}};
assert(validators.get('qag_resource_create')(resourceWrite), 'unnamed values must be ignored before validation');
resourceWrite.config.variables.GOOD.mode='invalid';
assert(!validators.get('qag_resource_create')(resourceWrite), 'named variable mode must be strict');
assert(validators.get('qag_variables_set')({ id: '00000000-0000-0000-0000-000000000001', revision: 1, variables: {}, secrets: { clear: ['TOKEN'] } }));
assert(!validators.get('qag_variant_update')({ runId: '00000000-0000-0000-0000-000000000001', variantId: '00000000-0000-0000-0000-000000000002', revision: 1, request: { expected: {} } }));
console.log(`MCP: ${names.length} tools, ${names.length * 2} schemas, documentation and ${operations.length} UI domain operations verified.`);
