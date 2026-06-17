import { useContext } from 'react'
import { ToastContext } from '../components/Toast'

export function useToast() {
  return useContext(ToastContext)
}
