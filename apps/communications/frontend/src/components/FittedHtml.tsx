import { useLayoutEffect, useRef, useState } from "react";
import { EMAIL_DESIGN_WIDTH, FIT_FRAME_HEIGHT, fitScale } from "./conversationHelpers";

/**
 * An e-mail's HTML in its sandboxed iframe, scaled to fit when the space is narrower
 * than a typical e-mail is wide (Messenger design N25: the side panel at 320 px). The
 * sandbox has no `allow-same-origin`, so the page cannot read the document's own
 * width; the frame is laid out at a design width of 600 px and scaled down by the
 * space it is given. Where there is room the frame is unscaled, as in the Inbox.
 */
export function FittedHtml({ title, srcDoc }: { title: string; srcDoc: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;
    const measure = () => setWidth(element.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const scale = fitScale(width);
  return (
    <div
      ref={ref}
      data-fit-scale={scale}
      style={{ width: "100%", overflow: "hidden", height: scale < 1 ? FIT_FRAME_HEIGHT * scale : undefined }}
    >
      <iframe
        title={title}
        sandbox=""
        srcDoc={srcDoc}
        className="message-html"
        style={
          scale < 1
            ? {
                width: EMAIL_DESIGN_WIDTH,
                height: FIT_FRAME_HEIGHT,
                transform: `scale(${scale})`,
                transformOrigin: "0 0",
              }
            : undefined
        }
      />
    </div>
  );
}
