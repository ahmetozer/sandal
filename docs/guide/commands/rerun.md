# Rerun

Kill previos container and re exec with same arguments

```bash
sandal rerun new-york
```

???+ note
    Sandal does not save any informations related to environment variable.  
    If you are executing container with passenv or envall arguments,
    environment variables information is getting from executed shell environment.  
    From different shell or session, if you forget to set those variables, your container
    will be miss those environment variables.
