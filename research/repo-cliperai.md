# waterboxdeveloper/CliperAi-

Source: https://github.com/waterboxdeveloper/CliperAi- | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A CLI tool that transforms long-form video into optimized social media clips. It takes a video file or YouTube URL as input and emits multiple short-form video clips with synchronized subtitles, optimized framing, and AI-generated captions.

## The pipeline, in order
1. **Download**: `src/downloader.py` (via `YoutubeDownloader`)
2. **Transcription**: `src/transcriber.py` (via `Transcriber`)
3. **Segmentation**: `src/clips_generator.py` (via `ClipsGenerator`)
4. **Caption Generation**: `src/copys_generator.py` (via `generate_copys_for_video`)
5. **Quality Validation**: `src/copys_generator.py` (via `Pydantic`-based validation)
6. **Computer Vision Optimization**: `src/video_exporter.py` (via `VideoExporter`)
7. **Video Engineering & Export**: `src/video_exporter.py` (via `VideoExporter`)

## Models, libraries and services

| What it uses | For which stage | Local or Paid API |
| :--- | :--- | :--- |
| WhisperX | Transcription | Local |
| FFmpeg | Audio extraction / Video engineering | Local |
| ClipsAI (TextTiling + BERT) | Segmentation | Local |
| Gemini (2.0 Flash Exp) | Caption Generation | Paid API (Google) |
| Claude (3.5 Sonnet) | Caption Generation | Paid API (Anthropic) |
| MediaPipe | Face Detection / Reframing | Local |
| OpenCV (cv2) | Video processing / Reframing | Local |

## Prompts
**System Prompt Base (`src/prompts/base_prompts.py`):**
```text
Eres un experto en crear copies virales para TikTok/Reels/Shorts.

Tu trabajo es analizar clips de video (usando sus transcripciones) y generar:
1. **Copy:** Caption optimizado con hashtags integrados (max 150 caracteres)
2. **Metadata:** Análisis predictivo del clip (engagement, viral potential, sentiment, etc.)

## Reglas CRÍTICAS:

### Formato del Copy:
- **CRÍTICO: MAX 150 CARACTERES** (límite estricto de TikTok - cuenta CADA letra/espacio/emoji)
- Mínimo 20 caracteres (suficiente contexto)
- DEBE incluir SIEMPRE estos hashtags: {mandatory_hashtags} (obligatorio, branding)
- Además de {mandatory_hashtags}, incluye {optional_hashtag_count} hashtag(s) relevante(s) al contenido
- Hashtags mezclados naturalmente, NO al final en bloque
- Incluye emojis relevantes (1-2 max, no abuses)

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necessary
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevant

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola persona? Este increíble truco asegura que todas las preguntas importantes sean respondidas #TechEvents {hashtag_example}"

✅ CORRECTO (142 caracteres):
"Mi breakup casi destruye mi chatbot project 💔. Esto es lo que aprendí sobre el emotional weight. #DevLife {hashtag_example}"

✅ CORRECTO con un solo hashtag (130 caracteres):
"Para que tu #AI entienda el contexto, no solo basta con feelings. Necesitas cognitive instruction set {mandatory_hashtags}"

**⚠️ IMPORTANTE - Límite de 150 caracteres:**
Si tu copy queda muy largo, PRIORIZA en este orden:
1. Mantén el mensaje principal (hook + valor)
2. SIEMPRE conserva {mandatory_hashtags} (obligatorio)
3. Reduce o elimina el hashtag adicional si es necesario
4. Mantén al menos 1 emoji si es relevante

**Ejemplos con conteo exacto:**

✅ CORRECTO (148 caracteres):
"¿Cansado de Q&As dominados? 🎤 Este truco asegura que TODAS las preguntas se respondan en tus meetups #TechEvents {hashtag_example}"

❌ MUY LARGO (165 caracteres - RECHAZADO):
"¿Estás cansado de que los Q&A sessions sean dominados por una sola person..."
```

## How it decides WHAT to clip
It uses the `ClipsAI` library via `ClipsGenerator.find_clips` in `src/clips_generator.py`. It employs the TextTiling algorithm with BERT embeddings to identify thematic shifts in the transcription. If no natural boundaries are found, it falls back to fixed-duration segments defined by `max_clip_duration` in `src/clips_generator.py`.

## How it decides framing / cropping
The project implements a machine learning-based reframing solution (described in README.md) that detects key visual elements (faces, subjects, focal points) and predicts subject movement to optimize crop windows for vertical video. Implementation details are located in `tests/spike_face_reframing.py` (as a validation spike using MediaPipe).

## Multi-pass or iteration
The `CopysGenerator` includes a parameter `MAX_COPY_GENERATION_ATTEMPTS` (default 3) in `docker-compose.example.yml`, suggesting it may retry caption generation if validation fails.

## Steps here that a transcript-first clipper would MISS
*   **Semantic Segmentation**: Uses BERT embeddings via ClipsAI to find thematic shifts rather than just silence or fixed time intervals.
*   **Opening Word Extraction**: `SubtitleGenerator.extract_opening_words_from_clip` extracts the exact words spoken at the start of a clip to be used as a "hook" in AI captions.
*   **Speaker Hashtag Extraction**: `SubtitleGenerator.extract_speaker_hashtags` scans the transcript for hashtags mentioned by the speaker to ensure brand consistency in captions.
*   **ML-Based Reframing**: Uses MediaPipe for face detection to dynamically adjust crop windows for vertical video.

## Worth stealing
*   **`src/prompts/base_prompts.py`**: The `SYSTEM_PROMPT` and `JSON_FORMAT_INSTRUCTIONS` for generating highly structured, multi-metadata JSON outputs from an LLM.
*   **`src/subtitle_generator.py`**: The `extract_opening_words_from_clip` function for creating high-engagement hooks.
*   **`src/utils/prompt_injector.py`**: The `inject_channel_config` function for dynamically replacing placeholders in prompts with user-specific branding.
*   **`src/transcriber.py`**: The logic for aligning WhisperX timestamps with a specific alignment model for word-level precision.
*   **Licence**: MIT License.
