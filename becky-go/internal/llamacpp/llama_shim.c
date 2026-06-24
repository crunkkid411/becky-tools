//go:build llamacgo

/* llama_shim.c - in-process llama.cpp completion (new API, llama.cpp ~2025/2026). */
#include "llama_shim.h"
#include "llama.h"
#include "ggml-backend.h"
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

static struct llama_model   *g_model = NULL;
static struct llama_context *g_ctx   = NULL;
static const struct llama_vocab *g_vocab = NULL;

int becky_llama_init(const char *model_path, const char *backend_dir,
                     int n_gpu_layers, int n_ctx) {
    static int backend_inited = 0;
    if (!backend_inited) {
        /* The CPU/CUDA backends are separate DLLs in modern llama.cpp and must be
         * loaded before the model, or load fails with "no backends are loaded". */
        if (backend_dir && backend_dir[0])
            ggml_backend_load_all_from_path(backend_dir);
        else
            ggml_backend_load_all();
        llama_backend_init();
        backend_inited = 1;
    }

    struct llama_model_params mp = llama_model_default_params();

    /* Degrade, never crash (the becky rule): try the requested GPU offload first, then
     * partial offload, then all-CPU, if VRAM is too tight to load fully on the GPU. Keeps
     * the model IN-PROCESS under VRAM pressure instead of failing out to the warm server. */
    int tries[3];
    int nt = 0;
    tries[nt++] = n_gpu_layers;
    if (n_gpu_layers < 0 || n_gpu_layers > 24) tries[nt++] = 24; /* partial */
    if (n_gpu_layers != 0) tries[nt++] = 0;                      /* all-CPU */
    for (int i = 0; i < nt; i++) {
        mp.n_gpu_layers = tries[i];
        g_model = llama_model_load_from_file(model_path, mp);
        if (g_model) {
            if (i > 0)
                fprintf(stderr, "shim: degraded to n_gpu_layers=%d (VRAM pressure)\n", tries[i]);
            break;
        }
    }
    if (!g_model) { fprintf(stderr, "shim: model load failed: %s\n", model_path); return 1; }
    g_vocab = llama_model_get_vocab(g_model);

    struct llama_context_params cp = llama_context_default_params();
    cp.n_ctx   = (unsigned)n_ctx;
    cp.n_batch = (unsigned)n_ctx;
    g_ctx = llama_init_from_model(g_model, cp);
    if (!g_ctx) { fprintf(stderr, "shim: context init failed\n"); return 2; }
    return 0;
}

int becky_llama_complete(const char *prompt, int max_tokens, float temp,
                         unsigned int seed, char *out, int out_cap) {
    if (!g_ctx || !g_model || !g_vocab) return -1;

    /* tokenize the prompt (add BOS + parse the chat special tokens) */
    int n_prompt = -llama_tokenize(g_vocab, prompt, (int)strlen(prompt), NULL, 0, true, true);
    if (n_prompt <= 0) return -2;
    llama_token *toks = (llama_token *)malloc(sizeof(llama_token) * n_prompt);
    if (!toks) return -3;
    if (llama_tokenize(g_vocab, prompt, (int)strlen(prompt), toks, n_prompt, true, true) < 0) {
        free(toks); return -4;
    }

    /* fresh KV cache for this completion */
    llama_memory_clear(llama_get_memory(g_ctx), true);

    /* sampler chain: greedy when temp<=0 (deterministic JSON), else temp + dist(seed) */
    struct llama_sampler *smpl = llama_sampler_chain_init(llama_sampler_chain_default_params());
    if (temp <= 0.0f) {
        llama_sampler_chain_add(smpl, llama_sampler_init_greedy());
    } else {
        llama_sampler_chain_add(smpl, llama_sampler_init_temp(temp));
        llama_sampler_chain_add(smpl, llama_sampler_init_dist(seed));
    }

    struct llama_batch batch = llama_batch_get_one(toks, n_prompt);
    llama_token new_tok;
    int generated = 0, written = 0;
    for (;;) {
        if (llama_decode(g_ctx, batch) != 0) { written = -5; break; }
        new_tok = llama_sampler_sample(smpl, g_ctx, -1);
        if (llama_vocab_is_eog(g_vocab, new_tok) || generated >= max_tokens) break;
        char piece[512];
        int np = llama_token_to_piece(g_vocab, new_tok, piece, (int)sizeof(piece), 0, true);
        if (np < 0) break;
        if (written + np < out_cap - 1) { memcpy(out + written, piece, np); written += np; }
        generated++;
        batch = llama_batch_get_one(&new_tok, 1);
    }
    if (written >= 0) out[written] = '\0';

    llama_sampler_free(smpl);
    free(toks);
    return written;
}

void becky_llama_free(void) {
    if (g_ctx)   { llama_free(g_ctx); g_ctx = NULL; }
    if (g_model) { llama_model_free(g_model); g_model = NULL; }
    g_vocab = NULL;
}
