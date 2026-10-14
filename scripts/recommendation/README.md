# Local semantic embedder for Meilisearch acceptance

These tools are optional QA infrastructure. They do not enable recommendations,
set an approval version, or alter the application's dependencies. They run a
real ONNX model on CPU; they do not manufacture embeddings. No original input,
vector, account ID, or authorization value is written to logs.

## Install and verify a model

Use Python 3.12 on Linux x86-64. Keep artifacts outside the checkout. The sample
uses `/tmp` because the original development root filesystem is full.

```sh
python3 -m venv /tmp/community-meili-acceptance/venv
PIP_CACHE_DIR=/tmp/community-meili-acceptance/pip-cache \
  /tmp/community-meili-acceptance/venv/bin/pip install \
  -r scripts/recommendation/embedding-requirements.txt
```

Review the selected repository and obtain its full commit revision from the
Hugging Face model API. Do not pass `main` or an unpinned release. The installer
accepts two public, multilingual models: `Xenova/multilingual-e5-small` (384 dimensions, quantized ONNX; its model
card identifies `intfloat/multilingual-e5-small` as the source but does not
declare a license itself. The reviewed upstream at
`614241f622f53c4eeff9890bdc4f31cfecc418b3` declares MIT) and
`Qdrant/paraphrase-multilingual-MiniLM-L12-v2-onnx-Q` (Apache-2.0,
384 dimensions, quantized ONNX). Both use masked mean pooling and L2 normalization.

```sh
python3 scripts/recommendation/prepare_embedding_model.py \
  --repo Xenova/multilingual-e5-small \
  --revision REVIEWED_40_CHARACTER_COMMIT \
  --output /tmp/community-meili-acceptance/model
```

The installer checks the revision and license against repository metadata
(and the pinned, explicitly identified upstream for the Xenova conversion), each
LFS object's SHA-256 or Git blob object ID, and artifact sizes. It records a
manifest; the server verifies both local file SHA-256 hashes at every startup.
It will not overwrite an existing model directory. Installation requires HTTPS
access to `huggingface.co` and its artifact redirect domains, commonly
`cdn-lfs.huggingface.co`, `cdn-lfs.hf.co`, `cas-bridge.xethub.hf.co`, and
`us.aws.cdn.hf.co`. Preserve
certificate validation and the environment's proxy/network policy.

## Run

```sh
/tmp/community-meili-acceptance/venv/bin/python \
  scripts/recommendation/serve_embeddings.py \
  --model-dir /tmp/community-meili-acceptance/model --port 17701 --threads 2
```

The listener is always `127.0.0.1`; it cannot be configured to listen publicly.
`GET /health` reports model/revision/dimensions and worker readiness.
`GET /metrics` reports aggregate request/input/error counts and inference times.
The private model worker uses no network client or model auto-loader.

Limits: 256 KiB request body, 16 inputs, 16 KiB per input, 256 tokens after
truncation, 8 HTTP connections, one active inference, 1 second maximum queue
wait and 8 seconds inference deadline. A timed-out worker is terminated and
requires a service restart. ONNX uses two CPU threads by default; supported
values are 1, 2, and 4. Each HTTP connection closes after the response.
These are local QA limits, not a production deployment or RSS quota.

## Meilisearch REST configuration

The following is the value of `RECOMMENDATION_EMBEDDER_CONFIG` for E5:

```json
{
  "source": "rest",
  "url": "http://127.0.0.1:17701/embed",
  "dimensions": 384,
  "request": {"input": ["{{text}}", "{{..}}"]},
  "response": {"data": [{"embedding": "{{embedding}}"}, "{{..}}"]},
  "documentTemplate": "passage: {{doc.text}}",
  "documentTemplateMaxBytes": 16000
}
```

The server retains the explicit `passage: ` document prefix and prefixes all
other E5 inputs with `query: `. This configuration is for Community's generated
public topic queries. For MiniLM use `{{doc.text}}` as the document template;
no query or passage prefixes are applied. Keep the model family, dimensions,
and template consistent. Changing any preprocessing requires a new model
version and index approval.

Set `RECOMMENDATION_EMBEDDER_MODEL_VERSION` to a value incorporating the model
revision, quantization, pooling, normalization, token limit, and server version,
for example `e5-small:<commit>:int8:mean-l2:tokens256:server-v1`. Also set the name,
document/hour budgets, and budget approval described in `docs/recommendations.md`.
Review the resulting full enhanced version before approving it. The hostname
must be reachable from the Meilisearch process; this example assumes it shares
the host network, not an isolated container.

Read the dated acceptance report for actual executed checks. Dependency
installation, a syntax check, or a successful health endpoint alone does not
establish model quality or successful Meilisearch hybrid retrieval.
