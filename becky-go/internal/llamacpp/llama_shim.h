/* llama_shim.h - tiny in-process llama.cpp completion API (de-risk scratch test). */
#ifndef BECKY_LLAMA_SHIM_H
#define BECKY_LLAMA_SHIM_H
#ifdef __cplusplus
extern "C" {
#endif

/* Load a GGUF once. backend_dir = the folder holding the ggml backend DLLs
 * (ggml-cpu and ggml-cuda); NULL or "" uses the default search. n_gpu_layers<0 =
 * all on GPU. Returns 0 on success, nonzero on error. */
int becky_llama_init(const char *model_path, const char *backend_dir,
                     int n_gpu_layers, int n_ctx);

/* Complete an already-chat-formatted prompt. temp<=0 = greedy (deterministic).
 * Writes up to out_cap-1 bytes + NUL into out. Returns #bytes written, or <0 on error. */
int becky_llama_complete(const char *prompt, int max_tokens, float temp,
                         unsigned int seed, char *out, int out_cap);

/* Free the model + context. */
void becky_llama_free(void);

#ifdef __cplusplus
}
#endif
#endif
