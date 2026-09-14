import { useState } from 'react'
import { AuthPanel } from './auth/AuthPanel'
import { CapturePanel } from './capture/CapturePanel'
import { LocationDashboard } from './dashboard/LocationDashboard'
import './App.css'

function App() {
  const [locationId, setLocationId] = useState<string | null>(null)

  return (
    <>
      <h1>FieldSync</h1>
      <AuthPanel />
      <CapturePanel onLocationCaptured={setLocationId} />
      {locationId && <LocationDashboard locationId={locationId} />}
    </>
  )
}

export default App
