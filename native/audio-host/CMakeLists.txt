# becky-audio-host - C++ audio + VST3 host sidecar (GUI-RULES.md Phases 2-3).
#
# Produces becky-audio-host(.exe): a standalone NDJSON-over-stdio control plane that
# hosts Jordan's VST3 plugins (MIT VST3 SDK) and outputs to his audio interface via
# PortAudio (WASAPI by default; ASIO when the Steinberg ASIO SDK is supplied).
#
# third_party/ is fetched locally (scripts/fetch-deps.ps1) and is gitignored. This
# CMakeLists compiles a MINIMAL slice of the VST3 SDK hosting helpers directly into the
# exe (no vstgui, no SDK samples) and links PortAudio as a static subproject.
cmake_minimum_required(VERSION 3.18)
project(becky_audio_host CXX C)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_C_STANDARD 11)
if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

set(TP "${CMAKE_CURRENT_SOURCE_DIR}/third_party")
set(VST3 "${TP}/vst3sdk")
set(PA "${TP}/portaudio")

if(NOT EXISTS "${VST3}/public.sdk/source/vst/hosting/module.h")
  message(FATAL_ERROR
    "VST3 SDK not found at ${VST3}. Run scripts/fetch-deps.ps1 first.")
endif()
if(NOT EXISTS "${PA}/CMakeLists.txt")
  message(FATAL_ERROR
    "PortAudio not found at ${PA}. Run scripts/fetch-deps.ps1 first.")
endif()

# ---------------------------------------------------------------------------
# PortAudio (MIT). WASAPI on by default; ASIO when the SDK is present.
# ---------------------------------------------------------------------------
set(PA_BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)
set(PA_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(PA_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(PA_USE_WASAPI ON CACHE BOOL "" FORCE)

# ASIO: only enable if Jordan supplied the SDK (account-gated download). Detect either
# BECKY_ASIO_SDK env var or third_party/asiosdk.
set(BECKY_ASIO_DIR "")
if(DEFINED ENV{BECKY_ASIO_SDK} AND EXISTS "$ENV{BECKY_ASIO_SDK}")
  set(BECKY_ASIO_DIR "$ENV{BECKY_ASIO_SDK}")
elseif(EXISTS "${TP}/asiosdk")
  set(BECKY_ASIO_DIR "${TP}/asiosdk")
endif()

if(BECKY_ASIO_DIR)
  message(STATUS "becky-audio-host: ASIO SDK found at ${BECKY_ASIO_DIR} -> ASIO enabled")
  set(PA_USE_ASIO ON CACHE BOOL "" FORCE)
  list(APPEND CMAKE_PREFIX_PATH "${BECKY_ASIO_DIR}")
  set(ASIO_ROOT "${BECKY_ASIO_DIR}" CACHE PATH "" FORCE)
  set(ASIO_SDK_DIR "${BECKY_ASIO_DIR}" CACHE PATH "" FORCE)
else()
  message(STATUS
    "becky-audio-host: ASIO SDK not found -- using WASAPI (drop the SDK in "
    "BECKY_ASIO_SDK for low-latency ASIO to the UR12)")
  set(PA_USE_ASIO OFF CACHE BOOL "" FORCE)
endif()

add_subdirectory("${PA}" portaudio EXCLUDE_FROM_ALL)

# ---------------------------------------------------------------------------
# Minimal VST3 SDK hosting slice (MIT). Compiled directly into the exe.
# ---------------------------------------------------------------------------
set(VST3_SOURCES
  # pluginterfaces base
  "${VST3}/pluginterfaces/base/conststringtable.cpp"
  "${VST3}/pluginterfaces/base/coreiids.cpp"
  "${VST3}/pluginterfaces/base/funknown.cpp"
  "${VST3}/pluginterfaces/base/ustring.cpp"
  # base
  "${VST3}/base/source/baseiids.cpp"
  "${VST3}/base/source/fbuffer.cpp"
  "${VST3}/base/source/fdebug.cpp"
  "${VST3}/base/source/fdynlib.cpp"
  "${VST3}/base/source/fobject.cpp"
  "${VST3}/base/source/fstreamer.cpp"
  "${VST3}/base/source/fstring.cpp"
  "${VST3}/base/source/updatehandler.cpp"
  "${VST3}/base/thread/source/fcondition.cpp"
  "${VST3}/base/thread/source/flock.cpp"
  # public.sdk common
  "${VST3}/public.sdk/source/common/commoniids.cpp"
  "${VST3}/public.sdk/source/common/commonstringconvert.cpp"
  "${VST3}/public.sdk/source/common/memorystream.cpp"
  "${VST3}/public.sdk/source/common/pluginview.cpp"
  # public.sdk vst hosting
  "${VST3}/public.sdk/source/vst/hosting/connectionproxy.cpp"
  "${VST3}/public.sdk/source/vst/hosting/eventlist.cpp"
  "${VST3}/public.sdk/source/vst/hosting/hostclasses.cpp"
  "${VST3}/public.sdk/source/vst/hosting/module.cpp"
  "${VST3}/public.sdk/source/vst/hosting/parameterchanges.cpp"
  "${VST3}/public.sdk/source/vst/hosting/pluginterfacesupport.cpp"
  "${VST3}/public.sdk/source/vst/hosting/plugprovider.cpp"
  "${VST3}/public.sdk/source/vst/hosting/processdata.cpp"
  # public.sdk vst utility
  "${VST3}/public.sdk/source/vst/utility/stringconvert.cpp"
  # canonical VST interface IID definitions (the SDK's single INIT_CLASS_IID TU)
  "${VST3}/public.sdk/source/vst/vstinitiids.cpp"
)
if(WIN32)
  list(APPEND VST3_SOURCES
    "${VST3}/public.sdk/source/vst/hosting/module_win32.cpp"
    "${VST3}/public.sdk/source/common/threadchecker_win32.cpp")
elseif(APPLE)
  list(APPEND VST3_SOURCES
    "${VST3}/public.sdk/source/vst/hosting/module_mac.mm"
    "${VST3}/public.sdk/source/common/threadchecker_mac.mm")
else()
  list(APPEND VST3_SOURCES
    "${VST3}/public.sdk/source/vst/hosting/module_linux.cpp"
    "${VST3}/public.sdk/source/common/threadchecker_linux.cpp")
endif()

# ---------------------------------------------------------------------------
# The executable.
# ---------------------------------------------------------------------------
add_executable(becky-audio-host
  src/main.cpp
  src/protocol.cpp
  src/audio_device.cpp
  src/vst_host.cpp
  src/plugin_probe.cpp
  ${VST3_SOURCES}
)

target_include_directories(becky-audio-host PRIVATE
  "${VST3}"
  "${PA}/include"
)

target_link_libraries(becky-audio-host PRIVATE portaudio)

# The VST3 SDK requires RELEASE or DEVELOPMENT to be defined.
target_compile_definitions(becky-audio-host PRIVATE
  $<$<CONFIG:Debug>:DEVELOPMENT=1>
  $<$<NOT:$<CONFIG:Debug>>:RELEASE=1>
)

if(WIN32)
  # COM (module_win32 dynamic load) + shell.
  target_link_libraries(becky-audio-host PRIVATE ole32 shell32 uuid)
  if(MINGW)
    # Self-contained exe for Jordan: static runtime.
    target_link_options(becky-audio-host PRIVATE
      -static -static-libgcc -static-libstdc++)
  endif()
endif()

message(STATUS "becky-audio-host: build type ${CMAKE_BUILD_TYPE}, ASIO=${PA_USE_ASIO}")
