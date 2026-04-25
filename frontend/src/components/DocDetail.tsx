import { useParams } from 'react-router-dom'

export default function DocDetail() {
  const { id } = useParams<{ id: string }>()

  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-4">Document {id}</h2>
      <p className="text-gray-600">View and edit document details.</p>
      <div className="mt-6 bg-gray-50 rounded-lg p-6">
        <p className="text-gray-500">Document content will be displayed here.</p>
      </div>
    </div>
  )
}