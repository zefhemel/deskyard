import type { ScriptWorkletNode } from "./contract.ts";

type Processor = {
  process(
    inputs: Float32Array[][],
    outputs: Float32Array[][],
    parameters: object,
  ): boolean;
};
type ProcessorClass = new (options: AudioWorkletNodeOptions) => Processor;
/** Local playback fallback for insecure contexts; never changes browser globals. */
export function playbackWorklets() {
  const registered = new WeakMap<AudioContext, Map<string, ProcessorClass>>();
  let port: MessagePort | null = null;
  class ProcessorBase {
    port = port;
  }
  return {
    async addWorklet(ctx: AudioContext, url: string) {
      if (ctx.audioWorklet) return ctx.audioWorklet.addModule(url);
      const response = await fetch(url);
      if (!response.ok)
        throw new Error(`Cannot load playback processor: ${response.status}`);
      const code = await response.text();
      let processors = registered.get(ctx);
      if (!processors) {
        processors = new Map();
        registered.set(ctx, processors);
      }
      const scope = {
        AudioWorkletProcessor: ProcessorBase,
        registerProcessor: (name: string, cls: ProcessorClass) =>
          processors.set(name, cls),
        sampleRate: ctx.sampleRate,
        get currentTime() {
          return ctx.currentTime;
        },
        get currentFrame() {
          return Math.round(ctx.currentTime * ctx.sampleRate);
        },
      };
      new Function("scope", `with (scope) {\n${code}\n}`)(scope);
    },
    workletNode(
      ctx: AudioContext,
      name: string,
      options: AudioWorkletNodeOptions = {},
    ): AudioWorkletNode | ScriptWorkletNode {
      if (ctx.audioWorklet) return new AudioWorkletNode(ctx, name, options);
      const cls = registered.get(ctx)?.get(name);
      if (!cls)
        throw new DOMException(
          `No playback processor "${name}"`,
          "InvalidStateError",
        );
      const channels = options.outputChannelCount?.[0] || 2;
      const node = ctx.createScriptProcessor(
        1024,
        1,
        channels,
      ) as ScriptWorkletNode;
      const channel = new MessageChannel();
      port = channel.port2;
      let processor: Processor;
      try {
        processor = new cls(options);
      } finally {
        port = null;
      }
      node.port = channel.port1;
      const input = [new Float32Array(128)];
      const output = Array.from(
        { length: channels },
        () => new Float32Array(128),
      );
      let running = true;
      node.onaudioprocess = (event) => {
        const source = event.inputBuffer.getChannelData(0);
        for (let at = 0; at < event.outputBuffer.length; at += 128) {
          input[0]!.set(source.subarray(at, at + 128));
          output.forEach((buffer) => buffer.fill(0));
          if (running) {
            try {
              running = processor.process([input], [output], {}) !== false;
            } catch (error) {
              running = false;
              console.warn("Deskyard playback processor:", error);
            }
          }
          output.forEach((buffer, index) =>
            event.outputBuffer.getChannelData(index).set(buffer, at),
          );
        }
      };
      return node;
    },
  };
}
