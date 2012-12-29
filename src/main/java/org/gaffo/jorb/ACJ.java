package org.gaffo.jorb;

import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.List;

public abstract class ACJ implements IJob
{

    @Override
    public final void process(IContext context)
    {
        Method[] methods = this.getClass().getMethods();
        List<Method> matches = new ArrayList<>();
        for(Method method : methods)
        {
            if (method.getName().equals("process"))
            {
                if (method.getParameterTypes().length == 1 && method.getParameterTypes()[0].equals(IContext.class))
                    continue;
                matches.add(method);
            }
        }

        if (matches.size() > 1)
        {
            throw new MultipleMatchingProcessMethodsException();
        }
        else if (matches.size() == 0)
        {
            throw new NoProcessMethodException();
        }

        Method method = matches.get(0);

        Object params[] = new Object[method.getParameterTypes().length];
        List<Class> missingParameterTypes = new ArrayList<>();
        for (int i = 0; i < params.length; ++i)
        {
            params[i] = context.get(method.getParameterTypes()[i]);
            if (params[i] == null)
            {
                missingParameterTypes.add(method.getParameterTypes()[i]);
            }
        }
        if (missingParameterTypes.size() > 0)
        {
            throw new RequiredParametersMissingFromContextException(missingParameterTypes);
        }

        try
        {
            method.invoke(this, params);
        }
        catch (IllegalAccessException e)
        {
            e.printStackTrace();
        }
        catch (InvocationTargetException e)
        {
            context.get(IJobErrorHandler.class).handleException(e.getTargetException(), this);
        }
        catch (Exception e)
        {
            context.get(IJobErrorHandler.class).handleException(e, this);
        }
    }

    public class BaseException extends RuntimeException
    {
    }

    public class NoProcessMethodException extends BaseException
    {
    }

    public class RequiredParametersMissingFromContextException extends BaseException
    {
        private final List<Class> missing;
        public RequiredParametersMissingFromContextException(List<Class> missing)
        {
            this.missing = missing;
        }

        public List<Class> missingParameterTypes()
        {
            return missing;
        }
    }

    public class MultipleMatchingProcessMethodsException extends BaseException
    {
    }
}
