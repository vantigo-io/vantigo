import type { ReactNode, SVGProps } from "react";

type Props = SVGProps<SVGSVGElement>;
// Purely decorative artwork: hidden from assistive technology, so no
// accessible name or translated title is needed.
const base = { width: 200, height: 160, viewBox: "0 0 200 160", fill: "none" } as const;
const Line = ({ children, ...props }: Props & { children?: ReactNode }) => (
  <svg aria-hidden="true" {...base} {...props}>
    {children}
  </svg>
);

export const NotFoundIllustration = (props: Props) => (
  <Line {...props}>
    <path
      d="M32 128h136M48 112l22-48 30 30 22-50 30 68"
      stroke="var(--mantine-color-blue-5)"
      strokeWidth="6"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
    <circle cx="70" cy="64" r="8" fill="var(--mantine-color-gray-3)" />
    <path d="M142 42v20m-10-10h20" stroke="var(--mantine-color-gray-6)" strokeWidth="4" strokeLinecap="round" />
  </Line>
);
export const ForbiddenIllustration = (props: Props) => (
  <Line {...props}>
    <rect
      x="55"
      y="70"
      width="90"
      height="62"
      rx="10"
      fill="var(--mantine-color-gray-1)"
      stroke="var(--mantine-color-blue-5)"
      strokeWidth="5"
    />
    <path
      d="M75 70V54a25 25 0 0 1 50 0v16M100 94v16"
      stroke="var(--mantine-color-gray-6)"
      strokeWidth="6"
      strokeLinecap="round"
    />
    <circle cx="100" cy="94" r="5" fill="var(--mantine-color-blue-5)" />
  </Line>
);
export const CrashIllustration = (props: Props) => (
  <Line {...props}>
    <path
      d="M100 24l12 30 32-5-20 26 24 22-34 2-14 34-13-34-35-2 24-22-20-26 32 5 12-30z"
      fill="var(--mantine-color-orange-0)"
      stroke="var(--mantine-color-orange-6)"
      strokeWidth="5"
      strokeLinejoin="round"
    />
    <path d="M94 62l-8 20h13l-5 18 18-27h-13l8-11" fill="var(--mantine-color-orange-6)" />
  </Line>
);
export const OfflineIllustration = (props: Props) => (
  <Line {...props}>
    <path
      d="M34 70c36-34 96-34 132 0M58 94c23-21 61-21 84 0M82 117c10-9 26-9 36 0"
      stroke="var(--mantine-color-blue-5)"
      strokeWidth="6"
      strokeLinecap="round"
    />
    <path d="M42 38l116 96" stroke="var(--mantine-color-gray-6)" strokeWidth="7" strokeLinecap="round" />
    <circle cx="100" cy="127" r="4" fill="var(--mantine-color-gray-6)" />
  </Line>
);
export const SessionIllustration = (props: Props) => (
  <Line {...props}>
    <path
      d="M74 30h52M74 130h52M82 30c0 25 36 25 36 50s-36 25-36 50M118 30c0 25-36 25-36 50s36 25 36 50"
      stroke="var(--mantine-color-blue-5)"
      strokeWidth="5"
      strokeLinecap="round"
    />
    <path
      d="M141 77l8 8-8 8m-9-8h17"
      stroke="var(--mantine-color-gray-6)"
      strokeWidth="4"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </Line>
);
export const MaintenanceIllustration = (props: Props) => (
  <Line {...props}>
    <path
      d="M124 42a25 25 0 0 0-31 31l-38 38a12 12 0 1 0 17 17l38-38a25 25 0 0 0 31-31l-17 17-14-4-4-14 18-16z"
      fill="var(--mantine-color-gray-1)"
      stroke="var(--mantine-color-blue-5)"
      strokeWidth="5"
      strokeLinejoin="round"
    />
    <circle cx="62" cy="119" r="3" fill="var(--mantine-color-gray-6)" />
  </Line>
);
