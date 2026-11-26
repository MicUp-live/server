import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { DEVELOPER_ENDPOINTS, CODE_SAMPLES } from './developerApiData.js'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const OPENAPI_JSON_PATH = path.resolve(__dirname, '../../public/openapi.json')
const CONTRACTS_DIR = path.resolve(__dirname, '../../../testdata/contracts')

describe('Developer API Documentation Contract Verification', () => {
    const openapi = JSON.parse(fs.readFileSync(OPENAPI_JSON_PATH, 'utf-8'))
    const scopeFixtures = JSON.parse(
        fs.readFileSync(path.join(CONTRACTS_DIR, 'developer_scope_failures_403.json'), 'utf-8')
    )

    // Derive authoritative route-to-scope expectations directly from executable contract fixtures
    // in testdata/contracts/developer_scope_failures_403.json.
    const FIXTURE_SCOPES = new Map()
    for (const testCase of scopeFixtures.cases) {
        const fixturePath = testCase.request.path
            .replace('/api/v1', '')
            .replace(/evt_contract_[0-9]+/, '{id}')
            .replace(/req_contract_[0-9]+/, '{rid}')
            .replace(/sb_contract_[0-9]+/, '{id}')
        const key = `${testCase.request.method} ${fixturePath}`
        FIXTURE_SCOPES.set(key, testCase.response.body.required_scope)
    }

    it('derives scope expectations directly from existing route-scope fixtures', () => {
        for (const ep of DEVELOPER_ENDPOINTS) {
            const cleanPath = ep.path.split('?')[0].replace('/api/v1', '')
            const opKey = `${ep.method} ${cleanPath}`

            if (ep.scope === 'Public') {
                const operation = openapi.paths[cleanPath]?.[ep.method.toLowerCase()]
                assert.ok(operation, `Path '${cleanPath}' must exist in OpenAPI`)
                const hasDevKey = (operation.security || []).some((sec) => 'DeveloperKeyAuth' in sec)
                assert.equal(hasDevKey, false, `Public endpoint '${opKey}' must not require developer key auth`)
            } else if (ep.scope === 'Bearer / Session') {
                // Host dashboard account endpoints (API key management)
                const operation = openapi.paths[cleanPath]?.[ep.method.toLowerCase()]
                assert.ok(operation, `Path '${cleanPath}' must exist in OpenAPI`)
                const hasDevKey = (operation.security || []).some((sec) => 'DeveloperKeyAuth' in sec)
                assert.equal(hasDevKey, false, `Session-scoped endpoint '${opKey}' must not accept raw developer keys`)
            } else {
                // Scoped developer key endpoint: must derive expectation directly from fixture
                assert.ok(
                    FIXTURE_SCOPES.has(opKey),
                    `Documented developer endpoint '${opKey}' must have authoritative contract fixture in developer_scope_failures_403.json; unverified handwritten scope assertions are prohibited`
                )
                const expectedScope = FIXTURE_SCOPES.get(opKey)
                assert.equal(
                    ep.scope,
                    expectedScope,
                    `Documented scope '${ep.scope}' for '${opKey}' must match authoritative contract fixture scope '${expectedScope}'`
                )
            }
        }
    })

    function resolveRef(refString) {
        if (!refString) return null
        const name = refString.replace('#/components/schemas/', '')
        const schema = openapi.components.schemas[name]
        assert.ok(schema, `Referenced schema '${name}' must exist in openapi.components.schemas`)
        return schema
    }

    // Resolves an endpoint operation and its declared response schema dynamically from OpenAPI
    function resolveResponseSchema(endpointPath, method, statusCode = '200') {
        const operation = openapi.paths[endpointPath]?.[method.toLowerCase()]
        assert.ok(operation, `Endpoint '${method} ${endpointPath}' must exist in OpenAPI`)
        const responseSpec = operation.responses?.[statusCode]
        assert.ok(responseSpec, `Endpoint '${method} ${endpointPath}' must declare response status '${statusCode}'`)
        const schemaRef = responseSpec.content?.['application/json']?.schema?.['$ref']
        assert.ok(schemaRef, `Response '${statusCode}' for '${method} ${endpointPath}' must reference a schema ($ref)`)
        const schemaName = schemaRef.replace('#/components/schemas/', '')
        const schema = openapi.components.schemas[schemaName]
        assert.ok(schema, `Schema '${schemaName}' must exist in openapi.components.schemas`)
        return { schemaName, schema }
    }

    // Validator for actual code sample contents, strictly binding each sample to the actual endpoint's schemas
    function validateSample(sampleType, codeString) {
        assert.ok(codeString && typeof codeString === 'string', 'Sample must be non-empty string')

        if (sampleType === 'curl') {
            // Extract curl invocations: method, path, and -d payloads
            const lines = codeString.split('\n')
            for (let i = 0; i < lines.length; i++) {
                const line = lines[i]
                if (!line.trim().startsWith('curl')) continue

                let command = line
                while (command.endsWith('\\') && i + 1 < lines.length) {
                    i++
                    command += ' ' + lines[i]
                }

                // Match method
                const methodMatch = command.match(/-X\s+([A-Z]+)/)
                const method = methodMatch ? methodMatch[1] : 'GET'

                // Match URL
                const urlMatch = command.match(/https:\/\/micup\.live(\/api\/v1\/[^\s"\\]+)/)
                assert.ok(urlMatch, `cURL command must contain valid URL: ${command}`)
                const rawPath = urlMatch[1].split('?')[0].replace('/api/v1', '')
                const normalizedPath = rawPath
                    .replace(/evt_[a-zA-Z0-9]+/, '{id}')
                    .replace(/req_[a-zA-Z0-9]+/, '{rid}')

                const pathItem = openapi.paths[normalizedPath]
                assert.ok(pathItem, `cURL path '${normalizedPath}' must exist in OpenAPI`)
                const operation = pathItem[method.toLowerCase()]
                assert.ok(operation, `cURL method '${method}' on '${normalizedPath}' must exist in OpenAPI`)

                // Match JSON payload and bind to the operation's actual requestBody schema
                const dataMatch = command.match(/-d\s+'([^']+)'/)
                if (dataMatch) {
                    const payload = JSON.parse(dataMatch[1])
                    const reqBodyRef = operation.requestBody?.content?.['application/json']?.schema?.['$ref']
                    assert.ok(reqBodyRef, `Operation '${method} ${normalizedPath}' must declare a requestBody schema`)
                    const schema = resolveRef(reqBodyRef)
                    const allowedProps = Object.keys(schema.properties || {})

                    for (const key of Object.keys(payload)) {
                        assert.ok(
                            allowedProps.includes(key),
                            `Payload field '${key}' in cURL sample must exist in ${reqBodyRef} schema`
                        )
                    }

                    if (payload.status) {
                        const statusEnum = schema.properties?.status?.enum || []
                        assert.ok(
                            statusEnum.includes(payload.status),
                            `Status '${payload.status}' in cURL sample must be a valid status enum`
                        )
                    }
                }
            }
        } else if (sampleType === 'python') {
            // Extract endpoint URL called in requests.get
            const urlMatch = codeString.match(/requests\.get\(\s*f"\{BASE_URL\}(\/events\/\{EVENT_ID\}\/[^"?]+)\?since=/)
            assert.ok(urlMatch, 'Python sample must call requests.get with URL')
            const normalizedPath = urlMatch[1].replace('{EVENT_ID}', '{id}')
            const method = 'GET'

            // Dynamically bind to the actual endpoint's response schema
            const { schemaName, schema: responseSchema } = resolveResponseSchema(normalizedPath, method)
            const schemaProps = responseSchema.properties || {}

            // Verify accessed top-level fields against the resolved endpoint schema
            const topLevelFieldMatches = [...codeString.matchAll(/data(?:\["([^"]+)"\]|\.get\("([^"]+)"\))/g)]
            const accessedTopLevel = topLevelFieldMatches.map((m) => m[1] || m[2])
            assert.ok(accessedTopLevel.length > 0, 'Python sample must access response fields')

            for (const field of accessedTopLevel) {
                assert.ok(
                    field in schemaProps,
                    `Field '${field}' accessed in Python sample does not exist on response schema '${schemaName}' for '${method} ${normalizedPath}'`
                )
            }

            // Host intake specifically requires 'requests' (array of incoming singer requests)
            assert.ok(
                'requests' in schemaProps,
                `Host intake polling in Python expects 'requests' property, which does not exist on response schema '${schemaName}' for '${method} ${normalizedPath}' (rejecting public rotation /queue)`
            )
            const itemsRef = schemaProps.requests.items?.['$ref']
            const itemSchema = resolveRef(itemsRef) || schemaProps.requests.items
            assert.ok(itemSchema, `Items schema for 'requests' must exist in '${schemaName}'`)

            // Verify accessed item fields
            const itemFieldMatches = [...codeString.matchAll(/req(?:\["([^"]+)"\]|\['([^']+)'\])/g)]
            const accessedItemFields = itemFieldMatches.map((m) => m[1] || m[2])
            for (const itemField of accessedItemFields) {
                assert.ok(
                    itemField in itemSchema.properties,
                    `Field '${itemField}' accessed on request item must exist on item schema '${itemsRef}'`
                )
            }

            // Verify status check
            const statusCheckMatch = codeString.match(/req\["status"\]\s*==\s*"([^"]+)"/)
            if (statusCheckMatch) {
                const statusVal = statusCheckMatch[1]
                assert.ok(
                    itemSchema.properties.status?.enum?.includes(statusVal),
                    `Status '${statusVal}' filtered in Python sample must be in '${itemsRef}' status enum`
                )
            }
        } else if (sampleType === 'csharp') {
            // Extract GET path
            const getMatch = codeString.match(/_http\.GetAsync\(\$"events\/\{_eventId\}(\/[^"?]+)\?since=/)
            assert.ok(getMatch, 'C# sample must call GetAsync with since parameter')
            const normalizedPath = '/events/{id}' + getMatch[1]
            const method = 'GET'

            // Dynamically bind to the actual endpoint's response schema
            const { schemaName, schema: responseSchema } = resolveResponseSchema(normalizedPath, method)
            const schemaProps = responseSchema.properties || {}

            // Extract the strongly-typed response DTO record used in ReadFromJsonAsync<T>
            const readJsonMatch = codeString.match(/ReadFromJsonAsync<([A-Za-z0-9_]+)>/)
            assert.ok(readJsonMatch, 'C# sample must call ReadFromJsonAsync with a strongly-typed DTO')
            const pollResultDtoName = readJsonMatch[1]

            const recordRegex = new RegExp(`public\\s+record\\s+${pollResultDtoName}\\s*\\(([\\s\\S]*?)\\);`)
            const recordMatch = codeString.match(recordRegex)
            assert.ok(recordMatch, `C# record definition for '${pollResultDtoName}' must exist in sample`)
            const recordBody = recordMatch[1]

            // Verify each JsonPropertyName on the DTO exists on the actual endpoint's response schema
            const propMatches = [...recordBody.matchAll(/\[property:\s*JsonPropertyName\("([^"]+)"\)\]/g)]
            const dtoProps = propMatches.map((m) => m[1])
            assert.ok(dtoProps.length > 0, `DTO '${pollResultDtoName}' must declare JsonPropertyName attributes`)

            for (const dtoProp of dtoProps) {
                assert.ok(
                    dtoProp in schemaProps,
                    `DTO property '${dtoProp}' declared in '${pollResultDtoName}' does not exist on response schema '${schemaName}' for '${method} ${normalizedPath}' (rejecting public rotation /queue)`
                )
            }

            // Host intake specifically requires 'requests'
            assert.ok(
                'requests' in schemaProps,
                `Host intake polling in C# expects 'requests' property, which does not exist on response schema '${schemaName}' for '${method} ${normalizedPath}' (rejecting public rotation /queue)`
            )
            const itemsRef = schemaProps.requests.items?.['$ref']
            const itemSchema = resolveRef(itemsRef) || schemaProps.requests.items
            assert.ok(itemSchema, `Items schema for 'requests' must exist in '${schemaName}'`)

            // Extract item DTO record definition (SingerRequest)
            const singerRecordMatch = codeString.match(/public\s+record\s+SingerRequest\s*\(([\s\S]*?)\);/)
            assert.ok(singerRecordMatch, `C# record definition for SingerRequest must exist in sample`)
            const singerRecordBody = singerRecordMatch[1]
            const itemPropMatches = [...singerRecordBody.matchAll(/\[property:\s*JsonPropertyName\("([^"]+)"\)\]/g)]
            const itemDtoProps = itemPropMatches.map((m) => m[1])

            for (const itemProp of itemDtoProps) {
                assert.ok(
                    itemProp in itemSchema.properties,
                    `DTO property '${itemProp}' in SingerRequest must exist on item schema '${itemsRef}'`
                )
            }

            // Verify status filter
            assert.ok(codeString.includes('req.Status == "pending"'))
            assert.ok(
                itemSchema.properties.status?.enum?.includes('pending'),
                "'pending' must be a valid status enum on Request"
            )

            // Verify PATCH path and method, binding to operation requestBody schema
            const patchMatch = codeString.match(/_http\.PatchAsJsonAsync\(\$"events\/\{_eventId\}(\/requests\/\{requestId\})/)
            assert.ok(patchMatch, 'C# sample must call PatchAsJsonAsync')
            const patchPath = '/events/{id}' + patchMatch[1].replace('{requestId}', '{rid}')
            const patchOp = openapi.paths[patchPath]?.patch
            assert.ok(patchOp, `C# PATCH endpoint '${patchPath}' must exist in OpenAPI`)

            const patchReqRef = patchOp.requestBody?.content?.['application/json']?.schema?.['$ref']
            assert.ok(patchReqRef, `Operation 'PATCH ${patchPath}' must declare requestBody schema`)
            const patchSchema = resolveRef(patchReqRef)

            assert.ok(codeString.includes('status = "up_next"'))
            assert.ok(patchSchema.properties?.status, 'status must exist on UpdateStatusRequest')
            assert.ok(
                patchSchema.properties.status.enum?.includes('up_next'),
                "'up_next' must be a valid status enum on UpdateStatusRequest"
            )
            assert.ok(codeString.includes('expected_queue_version = expectedQueueVersion'))
            assert.ok(
                patchSchema.properties?.expected_queue_version,
                'expected_queue_version must exist on UpdateStatusRequest'
            )
        }
    }

    it('validates actual displayed code samples (methods, paths, payload fields, status values)', () => {
        validateSample('curl', CODE_SAMPLES.curl)
        validateSample('python', CODE_SAMPLES.python)
        validateSample('csharp', CODE_SAMPLES.csharp)
    })

    describe('demonstrates that invalid samples fail validation', () => {
        it('fails on invalid HTTP method in cURL', () => {
            const badCurl = `curl -X PUT https://micup.live/api/v1/events/evt_01/requests/req_01`
            assert.throws(() => validateSample('curl', badCurl), /must exist in OpenAPI/)
        })

        it('fails on non-existent endpoint path in cURL', () => {
            const badCurl = `curl https://micup.live/api/v1/events/evt_01/unregistered_endpoint`
            assert.throws(() => validateSample('curl', badCurl), /must exist in OpenAPI/)
        })

        it('fails on unknown payload field in cURL', () => {
            const badCurl = `curl -s -X PATCH -d '{"status":"up_next","unknown_bogus_field":true}' https://micup.live/api/v1/events/evt_01/requests/req_01`
            assert.throws(() => validateSample('curl', badCurl), /must exist in #\/components\/schemas\/UpdateStatusRequest schema/)
        })

        it('fails on invalid status enum value in cURL', () => {
            const badCurl = `curl -s -X PATCH -d '{"status":"invalid_fake_status"}' https://micup.live/api/v1/events/evt_01/requests/req_01`
            assert.throws(() => validateSample('curl', badCurl), /must be a valid status enum/)
        })

        it('fails on invalid scope assertion against authoritative metadata', () => {
            const invalidDocEndpoint = {
                method: 'GET',
                path: '/api/v1/events/{id}/requests?since={v}',
                scope: 'wrong:scope',
            }
            const cleanPath = invalidDocEndpoint.path.split('?')[0].replace('/api/v1', '')
            const opKey = `${invalidDocEndpoint.method} ${cleanPath}`
            assert.throws(() => {
                assert.equal(invalidDocEndpoint.scope, FIXTURE_SCOPES.get(opKey))
            })
        })

        it('rejects Python polling sample changing host intake /requests to public rotation /queue', () => {
            const badPython = CODE_SAMPLES.python.replace(
                '/events/{EVENT_ID}/requests?since=',
                '/events/{EVENT_ID}/queue?since='
            )
            assert.throws(
                () => validateSample('python', badPython),
                /does not exist on response schema 'QueueResponse'/
            )
        })

        it('rejects C# polling sample changing host intake /requests to public rotation /queue', () => {
            const badCSharp = CODE_SAMPLES.csharp.replace(
                'events/{_eventId}/requests?since=',
                'events/{_eventId}/queue?since='
            )
            assert.throws(
                () => validateSample('csharp', badCSharp),
                /does not exist on response schema 'QueueResponse'/
            )
        })
    })
})
