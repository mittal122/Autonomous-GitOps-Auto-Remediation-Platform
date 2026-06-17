interface SkeletonProps {
  rows?: number
  className?: string
}

function SkeletonLine({ width = 'w-full', height = 'h-4' }: { width?: string; height?: string }) {
  return <div className={`${width} ${height} bg-gray-200 rounded animate-pulse`} />
}

export function SkeletonTable({ rows = 5 }: SkeletonProps) {
  return (
    <div className="rounded-lg border border-gray-200 overflow-hidden">
      <div className="bg-gray-50 px-4 py-3 flex gap-4">
        <SkeletonLine width="w-24" height="h-3" />
        <SkeletonLine width="w-16" height="h-3" />
        <SkeletonLine width="w-32" height="h-3" />
      </div>
      <div className="divide-y divide-gray-100">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className="px-4 py-3 flex gap-4 items-center bg-white">
            <SkeletonLine width="w-20" height="h-3" />
            <SkeletonLine width="w-12" height="h-5" />
            <SkeletonLine width="w-28" height="h-3" />
            <SkeletonLine width="w-36" height="h-3" />
          </div>
        ))}
      </div>
    </div>
  )
}

export function SkeletonCards({ rows = 3 }: SkeletonProps) {
  return (
    <div className="space-y-3">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="border border-gray-200 rounded-lg p-4 bg-white space-y-2">
          <SkeletonLine width="w-1/3" height="h-4" />
          <SkeletonLine width="w-2/3" height="h-3" />
          <SkeletonLine width="w-1/2" height="h-3" />
        </div>
      ))}
    </div>
  )
}

export function SkeletonStats() {
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 gap-4">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="border border-gray-200 rounded-lg p-4 bg-white space-y-2">
          <SkeletonLine width="w-16" height="h-3" />
          <SkeletonLine width="w-12" height="h-6" />
        </div>
      ))}
    </div>
  )
}
